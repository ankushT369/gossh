# gossh [![Build Status](https://github.com/ankushT369/gossh/actions/workflows/check-build.yml/badge.svg)](https://github.com/ankushT369/gossh/actions?query=workflow%3AC)

**gossh Lightweight SSH-over-HTTPS proxy for secure and firewall-friendly remote access**

gossh is a lightweight tool that enables SSH access over secure WebSocket (WSS) connections. It allows you to connect to remote machines even when direct SSH traffic (port 22) is blocked, by tunneling it through standard HTTPS infrastructure.

In many environments — such as corporate networks, cloud platforms, or public Wi-Fi — only HTTP/HTTPS traffic is allowed. gossh works by upgrading HTTP connections to WebSockets and streaming SSH data through them, enabling real-time, bidirectional communication without modifying the existing SSH server.

Instead of acting as a traditional HTTP proxy, gossh creates a persistent tunnel:

* The client exposes a local TCP port for SSH
* Data is forwarded over a secure WebSocket (WSS) connection
* The server bridges this to the local SSH daemon (`sshd`)
* Responses are streamed back instantly

This makes gossh behave like a raw TCP tunnel over WebSocket (WSS) using HTTPS infrastructure.

gossh is:

* **Lightweight** — minimal dependencies
* **Real-time** — full-duplex streaming
* **Firewall-friendly** — runs over HTTPS (port 443)
* **Transparent** — works with standard SSH clients

It does not replace SSH or require changes to the SSH server—only provides a flexible transport layer on top.

## How to use
### 0. Install
```bash
curl -fsSL https://raw.githubusercontent.com/ankushT369/gossh/main/install.sh | bash
```

###  1. Start gossh Server

Run the gossh server on your machine (where `sshd` is running):

```bash
gossh server --port 7777
```

<p align="center">
  <img src="docs/step1.png" alt="gossh" width="70%" />
</p>


> NOTE: Make sure SSH is running on port 22 (or specify using `--ssh`)


###  2. Expose Server using ngrok or cloudflare tunnel

Since gossh uses WebSockets over HTTP, you can expose it using:

```bash
ngrok http 7777
```

or 
```bash
cloudflared tunnel --url http://localhost:7777
```

<p align="center">
  <img src="docs/step2.png" alt="gossh" width="70%" />
</p>

[Click here for cloudflare tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/trycloudflare/)

[Click here for ngrok tunnel](https://ngrok.com/docs/share-localhost/overview)

**Copy the generated *HTTPS URL* (e.g., `https://xxxxx.trycloudflare.com`)**


###  3. Start gossh Client

On the client machine, connect to the server using the ngrok or cloudflare url:

```bash
gossh client \
  --connect https://your-tunnel.trycloudflare.com \
  --port 8888
```

**This creates a local TCP port (`8888`) for SSH access**


###  4. Connect via SSH

Now use standard SSH to connect:

```bash
ssh ankush@localhost -p 8888
```
<p align="center">
  <img src="docs/step4.png" alt="gossh" width="70%" />
</p>

**You are now connected to the remote machine through gossh!**


###  5. Access via Browser (Alternative, Client Not Required)

You can also access the terminal directly in your web browser without running `gossh client`:

1. Start the gossh server with `--web` specifying your public HTTPS origin:

```bash
gossh server --port 8000 --web https://xyz.com
```

2. Open the terminal in your browser by navigating to:

```text
https://xyz.com/term
```

Enter your SSH credentials to establish an interactive terminal session directly in the browser.


### Flow Summary

```text
Standard Client Mode:
SSH Client (localhost:8888)
        ↓
gossh Client
        ↓ (WSS over HTTPS via ngrok / cloudflare)
gossh Server
        ↓
sshd (localhost:22)

Browser Mode:
Browser (https://xyz.com/term)
        ↓ (WebSocket over HTTPS)
gossh Server
        ↓
sshd (localhost:22)
```


#### Notes
* ngrok is used only to expose the server publicly
* gossh itself handles the tunneling over WebSocket (WSS)
* Works in restricted networks where only HTTPS (port 443) is allowed


## Build
> Note: The project is in its inital phase recommended to build for linux amd64 or WSL
```bash
make
```

Output:

```
bin/gossh
```


## CLI reference

See [docs/cli-reference.md](docs/cli-reference.md) for the server, client, and status flags, defaults, and configuration precedence.


## Contribution
Everyone is welcome to contribute if you want to learn low-level network programming this can be helpful. Please make a different branch before any pull request.
