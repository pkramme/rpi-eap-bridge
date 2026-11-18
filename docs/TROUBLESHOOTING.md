# Troubleshooting

Start here: open the status page (<http://192.168.77.1>) and click **Run
connection test**. The first red line usually shows where the problem is.
After a failed login, the status page also explains the reason in plain words.

---

## Installation

**`ssh: Could not resolve hostname eap-bridge.local`**
Wait another two minutes; the first start is slow. Make sure your computer is on
the **same** phone hotspot as the Pi. If it still fails, look up the Pi's IP
address in your phone's hotspot settings (list of connected devices) and use
`ssh yourname@<that address>`.

**The Pi does not appear in the hotspot at all.**
- iPhone: switch on *Personal Hotspot → Maximise Compatibility*.
- Check the hotspot name and password you entered in Raspberry Pi Imager. Upper and lower case matter.
- Write the SD card again with Raspberry Pi Imager.

**`NetworkManager (nmcli) was not found`**
The operating system is too old. Write a current **Raspberry Pi OS** to the SD card with Raspberry Pi Imager.

**`Download failed` or `Could not install …`**
The Pi has no internet connection. Check the phone hotspot, or use the offline
method (Method B) in the [installation guide](INSTALL.md).

---

## The web interface does not open

1. Use **http://**192.168.77.1, not https://.
2. Your computer must be connected to the **Pi's Ethernet port** (directly or via the
   switch) with a cable. Use a different cable or switch port to rule out a bad cable.
3. Wait one minute after switching the Pi on.
4. Check that your computer got an address that starts with **192.168.77.**:
   - Windows: open PowerShell and type `ipconfig`.
   - Mac: *System Settings → Network → Ethernet → Details*.

   If it shows an address starting with 169.254, it did not get an address from the Pi.
   Unplug the cable, wait 10 seconds and plug it in again.
5. Did the installer mention **port 8080**? Then open <http://192.168.77.1:8080>.
6. Temporarily switch off your computer's WiFi, in case it confuses the connection.

**The page says "Please connect via Ethernet".**
You are trying to open it over the WiFi network. For security this is blocked as
soon as the Pi is on your organisation's network. Use a cable as described above.

---

## WiFi setup

**The list of networks is empty.**
- The status page may say that **WiFi is switched off** or that the **WiFi country is not set**.
  Choose your country under **Settings → WiFi country** and save.
- Click **Search again**.
- The Raspberry Pi 3 B, Zero W and Zero 2 W only support **2.4 GHz**. Networks that only
  use 5 GHz are invisible to them.

**"The network rejected the login: username or password are not accepted."**
- Use the **full** username, e.g. `jane.doe@university.edu`, not just `jane.doe`.
- Check the password. Tick **Show password** to see what you typed.
- Check the **inner authentication**: PEAP almost always uses MSCHAPv2, TTLS often PAP.
- Some organisations use a separate WiFi password (eduroam password, network password).
- Try logging in with the same details on your phone or laptop.
- After several failed attempts, some organisations lock the account temporarily.
  The bridge waits 30 minutes between automatic attempts after a rejected password, to avoid this.

**"The login server of the network could not be verified (certificate check failed)."**
- Check the **Server domain**. For `jane.doe@university.edu` it is usually `university.edu`.
  The error page shows the name the server reported (e.g. "radius.university.edu"). Its
  last part (`university.edu`) is a good value to try.
- If your IT department provides a **CA certificate** file, choose *Use a CA certificate
  file* and upload it.
- As a last resort choose **Don't check**. This works with almost every network but is
  less secure: a fake network with the same name could capture your password.

**"The network uses the login method TTLS/PEAP, but … is selected."**
Change **Method (EAP)** as suggested, check the inner authentication, and try again.

**"This network asks for a login with a personal certificate (EAP-TLS)."**
This bridge only supports username + password logins.

**"The login did not finish in time."**
Usually a wrong security setting (method, inner authentication, certificate check). It can also mean a
weak signal or a busy login server. The bridge keeps trying automatically.

**"The login server uses outdated encryption settings."**
The organisation's server uses an old TLS version that current Raspberry Pi OS
refuses. Only the IT department can fix this.

**The Pi keeps connecting to my phone hotspot instead.**
It does that only if your organisation's network is out of reach. Switch off the
hotspot. Without it, the bridge connects to your network automatically.

---

## Connected, but no internet

- **"Test website" shows a warning about a login page.** The network wants an extra
  login on a web page (a "captive portal"). On a device connected to the bridge, open
  <http://neverssl.com> and log in on the page that appears.
- **"Internet reachable" is red.** Some networks only allow registered devices.
  Your IT department may need the Pi's WiFi **MAC address**, shown under
  **Settings → About**.
- **"Name lookup (DNS)" is red, the line above is green.** The network's name
  server does not answer. Wait a few minutes; if it continues, contact IT.
- **Warning "same address range".** The WiFi network uses 192.168.77.x itself. Log in
  with `ssh`, run `sudo nano /etc/eap-bridge/config.json`, change `"lan_address"`
  to e.g. `"192.168.88.1/24"`, save (<kbd>Ctrl</kbd>+<kbd>O</kbd>, Enter,
  <kbd>Ctrl</kbd>+<kbd>X</kbd>), then restart the Pi. The web interface then moves to
  <http://192.168.88.1>.

## A device behind the bridge has no internet

- Set the device's network settings to **automatic / DHCP**. A fixed (static) IP address does not work.
- Unplug its cable for 10 seconds and plug it in again.
- Check that it appears under **Connected devices** on the status page.
- Check that the switch has power (its lights are on).
- VPN software on the device may need to be switched off or reconfigured.

## Slow connection

- Check the signal strength on the status page. Below 50 % the connection gets slower.
- Move the Pi nearer to a window or an access point, out of metal cabinets, and away
  from USB 3 hard drives (they disturb 2.4 GHz WiFi).
- A Raspberry Pi 4 or 5 is much faster than a Pi 3.

---

## Passwords

**I forgot the admin password of the web interface.**
Switch off the Pi, put the SD card into a computer and create an **empty file** named
`eap-bridge-reset-password` on the **bootfs** drive (on Windows, `eap-bridge-reset-password.txt`
works too). Put the card back and start the Pi. The password is removed.
Over SSH: `sudo eap-bridge reset-password`.

**I forgot the password to log in to the Pi (SSH).**
Write the SD card again with Raspberry Pi Imager and install again. The bridge itself keeps working
without it.

---

## Getting technical details

- In the web interface: **Technical log** (link at the bottom of every page).
- Over SSH:
  ```bash
  sudo eap-bridge status
  ```
  ```bash
  sudo journalctl -u eap-bridge -n 100
  ```
  ```bash
  journalctl -u NetworkManager -u wpa_supplicant -n 100
  ```
- Show these messages to your IT department. They contain no passwords.

## Starting over

```bash
sudo bash uninstall.sh --purge
```

This removes the bridge, the saved WiFi login and the Ethernet sharing. Or simply
write a fresh SD card with Raspberry Pi Imager.
