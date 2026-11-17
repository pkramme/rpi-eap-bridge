// Command eap-bridge connects a Raspberry Pi to a WPA-Enterprise (802.1X /
// EAP) WiFi network such as eduroam and shares the connection on its
// Ethernet port. It provides a small web interface for the setup.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pkramme/rpi-eap-bridge/internal/bridge"
	"github.com/pkramme/rpi-eap-bridge/internal/config"
	"github.com/pkramme/rpi-eap-bridge/internal/demo"
	"github.com/pkramme/rpi-eap-bridge/internal/nm"
	"github.com/pkramme/rpi-eap-bridge/internal/system"
	"github.com/pkramme/rpi-eap-bridge/internal/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usageText = `eap-bridge – share an enterprise (EAP) WiFi connection over Ethernet

Usage:
  eap-bridge [serve] [--config FILE] [--listen ADDR] [--demo]
                                 run the bridge and its web interface (as root)
  eap-bridge status              print the current state
  eap-bridge reset-password      remove the admin password of the web interface
  eap-bridge version             print the version

Try the web interface on any computer without changing anything:
  eap-bridge serve --demo        (then open http://127.0.0.1:8080)
`

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "status":
		err = status(args)
	case "reset-password":
		err = resetPassword(args)
	case "version":
		fmt.Println(version)
	case "help":
		fmt.Print(usageText)
	default:
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "eap-bridge:", err)
		os.Exit(1)
	}
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	return fs
}

func serve(args []string) error {
	fs := newFlagSet("serve")
	cfgPath := fs.String("config", config.DefaultPath, "configuration file")
	listen := fs.String("listen", "", "listen address (overrides the configuration), e.g. :8080")
	demoMode := fs.Bool("demo", false, "simulate NetworkManager to try the web interface on any computer")
	_ = fs.Parse(args)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	opts := bridge.Options{
		ConnectionsDir: "/etc/NetworkManager/system-connections",
		DataDir:        filepath.Dir(*cfgPath),
		Logger:         logger,
	}
	var (
		nmClient nm.NM
		sys      system.System
	)
	if *demoMode {
		dir, err := os.MkdirTemp("", "eap-bridge-demo-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		sim := demo.New()
		nmClient, sys = sim, sim
		*cfgPath = filepath.Join(dir, "config.json")
		opts.ConnectionsDir = filepath.Join(dir, "system-connections")
		opts.DataDir = dir
		if *listen == "" {
			*listen = "127.0.0.1:8080"
		}
		logger.Info("demo mode: NetworkManager is simulated", "files", dir)
	} else {
		if os.Geteuid() != 0 {
			return errors.New("the bridge must run as root (it changes the network configuration). Try: sudo eap-bridge serve")
		}
		nmClient, sys = &nm.Nmcli{}, &system.Linux{}
	}

	store, err := config.Open(*cfgPath)
	if err != nil {
		return err
	}
	if !*demoMode {
		consumeResetFile(store, logger)
	}
	cfg := store.Get()
	addr := cfg.Listen
	if *listen != "" {
		addr = *listen
	}

	mgr := bridge.New(nmClient, sys, store, opts)
	srv, err := web.New(mgr, store, sys, web.Options{Version: version, Logger: logger, Demo: *demoMode})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w (is another web server running? Change \"listen\" in %s)", addr, err, store.Path())
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go mgr.Run(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	logger.Info("eap-bridge started", "version", version, "listen", ln.Addr().String(), "lan_url", "http://"+cfg.LANIP()+"/")
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Info("eap-bridge stopped")
	return nil
}

// resetFiles can be created on the SD card's boot partition (readable on
// Windows and macOS) to remove a forgotten admin password.
var resetFiles = []string{
	"/boot/firmware/eap-bridge-reset-password",
	"/boot/firmware/eap-bridge-reset-password.txt",
	"/boot/eap-bridge-reset-password",
	"/boot/eap-bridge-reset-password.txt",
}

func consumeResetFile(store *config.Store, log *slog.Logger) {
	for _, p := range resetFiles {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := store.Update(func(c *config.Config) error { c.AdminPasswordHash = ""; return nil }); err != nil {
			log.Error("could not remove the admin password", "error", err)
			return
		}
		if err := os.Remove(p); err != nil {
			log.Warn("could not delete the reset file", "file", p, "error", err)
		}
		log.Warn("admin password removed because a reset file was found", "file", p)
	}
}

func status(args []string) error {
	fs := newFlagSet("status")
	cfgPath := fs.String("config", config.DefaultPath, "configuration file")
	_ = fs.Parse(args)
	if os.Geteuid() != 0 {
		return errors.New("please run with sudo: sudo eap-bridge status")
	}
	store, err := config.Open(*cfgPath)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mgr := bridge.New(&nm.Nmcli{}, &system.Linux{}, store, bridge.Options{Logger: logger, DataDir: filepath.Dir(*cfgPath)})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mgr.DetectInterfaces(ctx)
	st := mgr.Status(ctx)

	if st.NMError != "" {
		fmt.Println("Problem:      ", st.NMError)
	}
	network := "(not set up)"
	if st.Network != nil {
		network = fmt.Sprintf("%s (user %s, %s/%s)", st.Network.SSID, st.Network.Identity, strings.ToUpper(st.Network.EAP), st.Network.Phase2)
	}
	fmt.Println("WiFi network: ", network)
	fmt.Printf("WiFi state:    %s (%s)\n", st.WiFi.Label(), st.WiFi.Interface)
	if len(st.WiFi.IPv4) > 0 {
		fmt.Println("WiFi address: ", strings.Join(st.WiFi.IPv4, ", "))
	}
	if st.WiFi.Signal >= 0 {
		fmt.Printf("Signal:        %d%%\n", st.WiFi.Signal)
	}
	fmt.Printf("Ethernet:      %s (%s, %s)\n", st.LAN.Label(), st.LAN.Interface, st.LAN.Prefix)
	fmt.Printf("Devices:       %d\n", len(st.LAN.Clients))
	if st.Country != "" {
		fmt.Println("WiFi country: ", st.Country)
	} else {
		fmt.Println("WiFi country:  not set")
	}
	for _, w := range st.Warnings {
		fmt.Println("Warning:      ", w)
	}
	fmt.Printf("Web interface: http://%s/\n", st.LAN.Address)
	return nil
}

func resetPassword(args []string) error {
	fs := newFlagSet("reset-password")
	cfgPath := fs.String("config", config.DefaultPath, "configuration file")
	_ = fs.Parse(args)
	if os.Geteuid() != 0 {
		return errors.New("please run with sudo: sudo eap-bridge reset-password")
	}
	store, err := config.Open(*cfgPath)
	if err != nil {
		return err
	}
	if err := store.Update(func(c *config.Config) error { c.AdminPasswordHash = ""; return nil }); err != nil {
		return err
	}
	fmt.Println("The admin password was removed.")
	// Restart the service so it does not keep the old password in memory.
	if err := exec.Command("systemctl", "try-restart", "eap-bridge.service").Run(); err != nil {
		fmt.Println("Please restart the service: sudo systemctl restart eap-bridge")
	}
	return nil
}
