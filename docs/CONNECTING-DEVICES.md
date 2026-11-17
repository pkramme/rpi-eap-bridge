# Connecting your devices

The bridge passes its internet connection on through the Raspberry Pi's
**Ethernet port**. This page explains how to connect devices to it: one device,
several wired devices through a **switch**, and devices that only have WiFi.

```
   Organisation's WiFi (e.g. eduroam)
                │  WiFi with username + password
        ┌───────┴────────┐
        │  Raspberry Pi  │   address on the Ethernet side: 192.168.77.1
        └───────┬────────┘
                │  network cable
         ┌──────┴──────┐
         │   Switch    │   simple "unmanaged" switch, no setup needed
         └─┬───┬───┬───┘
           │   │   │
          PC  TV  Game console …   get an address automatically (192.168.77.x)
```

---

## Option 1 – One device

Connect the device directly to the Pi's Ethernet port with a network cable.
Done. The device gets an address and internet access automatically.

## Option 2 – Several wired devices: use a switch

A **switch** is a small box with several network ports. It connects
everything plugged into it.

### What to buy

- An **unmanaged Gigabit switch** with 5 or 8 ports. These cost about 15–25 €/$.
  Common models include the TP‑Link TL‑SG105 or LS1005G, the Netgear GS305 and the
  D‑Link DGS‑105, but any brand works.
- "Unmanaged" means there is **nothing to set up**. You do **not** need a "managed",
  "smart" or "PoE" switch.
- **Network cables** (Cat 5e or Cat 6), one per device plus one for the Pi.

### How to connect it

1. Connect the switch to its power supply.
2. Connect the **Pi's Ethernet port** to **any port** of the switch. All ports are
   the same, so there is no special port to use.
3. Connect each device to one of the remaining ports.
4. Make sure the devices' network settings are on **automatic** (also called
   *DHCP* or *obtain an address automatically*). This is the default almost everywhere.
5. Open the status page (<http://192.168.77.1>). Under **Ethernet sharing →
   Connected devices** you see every device that received an address.

You can add more switches later: just connect a port of the new switch to a port of
the old one.

### Typical devices

| Device | What to do |
|---|---|
| Desktop PC, laptop | Plug in the cable. Nothing else to do. |
| PlayStation / Xbox / Switch (dock) | Network settings → *wired connection* → *easy / automatic*. |
| Smart TV, streaming box | Network settings → *wired* → *automatic*. |
| Printer | Plug in the cable. The printer receives an address from the Pi. Computers on the same switch can find it. |

---

## Option 3 – Devices that only have WiFi

Many devices cannot log in to WiFi with a username and password: for example
Nintendo Switch, Chromecast, smart speakers, e-book readers, older laptops and
many smart TVs. Others have no Ethernet port at all.

For these, add your **own WiFi access point** behind the bridge. It creates a
normal WiFi network with a simple password, which every device understands:

```
   eduroam ))) Raspberry Pi ── Switch ──┬── PC
                                        ├── TV
                                        └── Travel router ((( "MyRoomWiFi"
                                                                 ├── Nintendo Switch
                                                                 ├── Chromecast
                                                                 └── Phone, Tablet …
```

Any WiFi router works. A small **travel router** (about 20–40 €/$) is the easiest
choice, and an old home router you no longer need works too.

### 3a – Access point mode (recommended)

In this mode the router only provides WiFi. All devices get their address from
the Pi, so every device can see every other device. For example, your phone can
cast to the TV.

1. Look for **"Access Point"**, **"AP mode"** or **"Bridge mode"** in the router's
   settings or manual. Many travel routers have a switch or menu for it.
2. Switch the router to that mode, then set your own **WiFi name** and a **strong password**.
3. Connect a **LAN port** of the router (not "WAN"/"Internet") to the switch or directly to the Pi.

### 3b – Router mode (if your router has no access point mode)

1. Connect the router's **WAN / Internet** port to the switch or to the Pi.
2. Set your own WiFi name and a strong password as usual.

The router receives internet from the Pi and builds its **own** small network.
Browsing, streaming and gaming work. Devices on the router's WiFi cannot see the
wired devices on the switch, so casting between them is not possible.

> [!CAUTION]
> In router mode, **never** connect a *LAN* port of the router to the switch. Two
> devices would hand out addresses, and nothing would work reliably.

> [!NOTE]
> Many universities and companies **forbid private WiFi access points**, because
> they interfere with the official WiFi. Check the rules first, or ask your IT department.

**Why can't the Raspberry Pi make the WiFi itself?** Its single built-in WiFi
chip is busy keeping the connection to your organisation's network. It cannot
reliably serve a second network at the same time. A separate access point is
faster and more stable.

---

## Rules for a trouble-free setup

- **Never connect the Pi's Ethernet port to a wall socket or to a router.** The Pi
  hands out addresses on that port. In a university or company network, that
  disturbs other people, and IT departments notice it quickly.
- Only connect **your own devices**. Everything counts as traffic from your account.
- Leave the devices on **automatic network settings (DHCP)**. Addresses
  192.168.77.2–192.168.77.254 are handed out automatically.

## What to expect

- **Speed** depends mainly on the WiFi signal and the Pi model. With a Raspberry Pi
  4 or 5 and a good signal, 50–200 Mbit/s is typical. That is enough for HD and 4K
  streaming. A Pi 3 B manages about 20–40 Mbit/s.
- **Devices behind the bridge cannot be reached from the WiFi network.** The Pi
  shares one connection, like a home router does. You cannot cast from a phone
  on eduroam to a TV behind the bridge. Connect the phone to your own access point
  (option 3a) instead.
- **Game consoles** often report a "strict" or "type 3" NAT. Online play and
  downloads normally work, but some voice-chat and peer-to-peer features can be
  limited. Port forwarding is not available, and most organisation networks block
  incoming connections anyway.
- The Ethernet side uses **IPv4** only, which every device supports.
