# Installation guide – step by step

This guide turns a Raspberry Pi into a small box that logs in to your
organisation's WiFi (for example **eduroam** at a university, or a company
network where you log in with a username and password) and passes the
internet on through a **network cable**. You need no prior knowledge.
Plan about **45 minutes**.

> [!IMPORTANT]
> **Check that this is allowed first.** Many universities and companies forbid
> sharing their network with other people or connecting your own network
> equipment. Only connect **your own devices**. Everything that goes through
> the bridge counts as traffic from **your** account.

---

## What you need

| Item | Notes |
|---|---|
| **Raspberry Pi** with WiFi and an Ethernet port | Recommended: **Raspberry Pi 4 Model B** or **Raspberry Pi 5**. A Raspberry Pi 3 B+ also works well. A Pi 3 B works but is slower and only supports 2.4 GHz WiFi. A Pi Zero 2 W needs a USB Ethernet adapter. |
| **Power supply** | Use the official one: USB‑C 15 W for the Pi 4, USB‑C 27 W for the Pi 5, micro‑USB 12.5 W for the Pi 3. Weak phone chargers cause strange errors. |
| **microSD card** | 16 GB or more. |
| **Network cables** | Normal "LAN" or "Ethernet" cables (Cat 5e or Cat 6). |
| **A computer** | Windows, Mac or Linux, with an SD card reader (or a USB card reader). |
| **Your WiFi login** | Username and password. Ideally also your organisation's WiFi instructions (they mention things like "PEAP" or "MSCHAPv2"). |
| **Temporary internet for the Pi** | Easiest: the **hotspot of your phone**, for about 10 minutes. If that is not possible, see [Method B](#method-b-offline-from-the-sd-card). |
| Optional: a **network switch** | To connect several devices. See [Connecting your devices](CONNECTING-DEVICES.md). |

---

## Step 1 – Prepare the SD card

1. On your computer, download and install **Raspberry Pi Imager** from
   <https://www.raspberrypi.com/software/>. Then start it.
2. **Device:** choose your Raspberry Pi model.
3. **Operating system:** choose *Raspberry Pi OS (other)* →
   **Raspberry Pi OS Lite (64-bit)**.
   *(Only for the very old Pi Zero W or Pi 1: take the 32-bit Lite version.)*
4. **Storage:** choose your microSD card. Take care to pick the right drive:
   everything on it will be erased.
5. **Customisation:** fill in the following. Raspberry Pi Imager 2 shows these
   as separate steps. In older Imager versions, click **Edit settings**.

   | Setting | What to enter |
   |---|---|
   | **Hostname** | `eap-bridge` |
   | **Localisation** | Your capital city / time zone and keyboard layout. This also sets the **WiFi country**, which is required. |
   | **User** | Choose a username and a password and **write them down**. You need them in step 2. |
   | **Wi‑Fi** | For [Method A](#method-a-via-your-phones-hotspot-recommended): the name and password of your **phone's hotspot** – *not* eduroam or your company WiFi. For Method B: leave it empty. |
   | **Remote access** | Switch **SSH** on and choose **password authentication**. |
   | Raspberry Pi Connect | Not needed. |

   > **iPhone hotspot tip:** under *Settings → Personal Hotspot* switch on
   > **Maximise Compatibility**. Otherwise some Pi models cannot see the hotspot.

6. Click **Write** and confirm. When Imager says the card is ready, take it out.

---

## Step 2 – Install the EAP Bridge software

Choose **one** of the two methods.

### Method A: via your phone's hotspot (recommended)

You type two commands into your computer. The Pi uses your phone's hotspot
to download the software.

1. **Switch on your phone's hotspot.**
2. Put the SD card into the Raspberry Pi and connect the power supply.
   **Wait 3–5 minutes.** The first start takes longer and the Pi restarts once
   on its own.
3. Connect your **computer to the same phone hotspot**.
4. Open a command window:
   - **Windows:** click Start, type `PowerShell` and open it.
   - **Mac:** press <kbd>⌘</kbd> + <kbd>Space</kbd>, type `Terminal` and press Enter.
5. Type the following. Replace `yourname` with the username from step 1, then press Enter:

   ```bash
   ssh yourname@eap-bridge.local
   ```

   - If you are asked *"Are you sure you want to continue connecting?"*, type `yes` and press Enter.
   - Enter your password. **Nothing appears while you type – that is normal.**
   - If you see *"Could not resolve hostname"*: look up the Pi's IP address in your phone's
     hotspot settings (connected devices) and type `ssh yourname@` followed by that address, e.g.
     `ssh yourname@172.20.10.5`.

6. You are now logged in to the Pi. Copy this command, paste it in (right‑click in
   PowerShell, <kbd>⌘</kbd> + <kbd>V</kbd> in Terminal) and press Enter:

   ```bash
   curl -fsSL https://github.com/YOUR-GITHUB-USER/rpi-eap-bridge/releases/latest/download/install.sh | sudo bash
   ```

   Enter your password again if asked. After a minute or two the installer
   shows **"Done! EAP Bridge is installed and running."**

7. Leave the window open for now and continue with step 3.

### Method B: offline from the SD card

Use this if the Pi cannot get any temporary internet connection. You need a
**keyboard and a screen** for the Pi. The Pi 4 and 5 use a **micro‑HDMI** cable.

1. On your computer, open the project's **Releases** page and download the
   newest `eap-bridge-<version>.zip`. Unzip it. You get a folder called `eap-bridge`.
2. After Raspberry Pi Imager has finished, put the SD card back into your
   computer. A drive called **bootfs** appears.
3. Copy the whole **`eap-bridge`** folder onto the **bootfs** drive. Then eject
   the card safely (Windows: "Eject", Mac: drag the drive to the bin).
4. Put the card into the Pi. Connect keyboard, screen and finally the power
   supply. Wait until a login prompt appears (this takes a few minutes the first time).
5. Log in with the username and password from step 1. The password stays invisible while you type.
6. Type the following command and press Enter:

   ```bash
   sudo bash /boot/firmware/eap-bridge/install.sh
   ```

   If the installer asks for your **country**, type the two-letter code (for
   example `DE`, `GB`, `US`, `NL`) and press Enter.

> If the installer reports that some packages are missing and cannot be
> installed, it needs internet once. Use Method A instead.

---

## Step 3 – Open the web interface

> [!WARNING]
> **Never plug the Pi's Ethernet port into a router or a network socket in the wall.**
> The Pi hands out network addresses on this port. In someone else's network that
> causes trouble for everybody. Only connect your own devices or your own switch.

1. Connect your computer to the **Ethernet port of the Raspberry Pi** with a
   network cable, either directly or through your switch.
   (Laptops without an Ethernet port need a USB Ethernet adapter.)
2. Open a web browser and go to:

   **<http://192.168.77.1>**

   Alternatively, **<http://eap-bridge.local>** often works too.

The **status page** of the bridge appears.

> **Tip for Method A:** while the Pi is still connected to your phone's hotspot,
> you can also open <http://eap-bridge.local> from a computer on that hotspot. As
> soon as the Pi has joined your organisation's WiFi, the page is only reachable
> through the Ethernet port. This protects it from other people on that network.

---

## Step 4 – Set up the WiFi

1. Click **Set up WiFi**.
2. **Network:** choose your organisation's network, for example `eduroam`. Networks
   that only need a password (like most home WiFi networks) are greyed out:
   this bridge is made for networks that need a username.
   Your network is not listed? Click **Search again**, or choose **Other network** and type its name.
3. **Username:** usually the **full** form, for example `jane.doe@university.edu`.
4. **Password:** your WiFi password.
5. **Security settings:** the defaults (**PEAP** with **MSCHAPv2**, *Check the
   server's domain*) fit most organisations. Your IT department's WiFi
   instructions tell you if anything differs. Look for these words in them:

   | In the instructions | Field in the web interface |
   |---|---|
   | EAP method, "Security: PEAP" or "TTLS" | **Method (EAP)** |
   | Phase 2, inner authentication: "MSCHAPv2", "PAP" | **Inner authentication** |
   | CA certificate, root certificate | **Use a CA certificate file** – upload the file |
   | Domain, server name, RADIUS server, e.g. `radius.uni.edu` | **Server domain**, e.g. `uni.edu` |
   | Anonymous identity, outer identity | **Advanced → Anonymous identity** |

   **eduroam users:** the official tool <https://cat.eduroam.org> lists the settings
   of your institution. When you leave **Server domain** empty, the part after the "@"
   in your username is used. That is correct for most universities.

6. Click **Save and connect**. The status page shows the progress. Connecting
   usually takes **10 to 40 seconds**.

If the connection fails, the page explains **why** in plain words and what to
change, for example *"The network rejected the login"* or *"The network uses
the login method TTLS"*. Click **Check the WiFi settings**, fix the setting and
try again. More help: [Troubleshooting](TROUBLESHOOTING.md).

---

## Step 5 – Test the connection

1. On the status page, click **Run connection test**. The bridge checks every link
   of the chain one by one:

   | Line | Meaning |
   |---|---|
   | WiFi connection | The Pi is logged in to your WiFi. Signal strength below 35 % shows a warning. |
   | IP address | The WiFi network gave the Pi an address. |
   | Internet reachable | The Pi can reach servers on the internet. |
   | Name lookup (DNS) | Web addresses can be translated into server addresses. |
   | Test website | A small test page loads. A warning here often means a login page ("captive portal"). |
   | Ethernet cable / Internet sharing | A cable is plugged in and the Pi shares the internet on it. |
   | Connected devices | How many devices received an address from the Pi. |

2. Everything green? Open any website on the computer connected to the Pi. 🎉

---

## Step 6 – Finish

- **Method A:** switch off your phone's hotspot. Type `exit` in the command window.
- **Optional:** set an **admin password** for the web interface under **Settings**,
  especially if other people use the same switch.
- Put the Pi where the WiFi signal is good, for example near a window, not in a metal cabinet.
- Connect your devices. See **[Connecting your devices](CONNECTING-DEVICES.md)**:
  it covers switches and devices that only have WiFi.
- The bridge starts automatically whenever the Pi is powered on. It reconnects on its own
  after WiFi interruptions.
- Before unplugging the power, use **Settings → Shut down** whenever you can. This protects the SD card.

---

## Updating

- **Method A:** connect the Pi to the internet as before, log in with `ssh` and run the
  same `curl … | sudo bash` command again.
- **Method B:** copy the `eap-bridge` folder from the newer ZIP onto the SD card,
  replacing the old one, and run `sudo bash /boot/firmware/eap-bridge/install.sh` again.

Your settings are kept.

## Uninstalling

```bash
sudo bash /boot/firmware/eap-bridge/uninstall.sh
```

Or download `uninstall.sh` from the Releases page. Plain `uninstall.sh` only removes
the web interface; the WiFi login and the sharing keep working. Run `uninstall.sh --purge`
to delete everything, including the saved WiFi login.
