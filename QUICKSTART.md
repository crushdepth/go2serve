# Quickstart

Serve a directory over HTTP in three steps.

## 1. Clone and build

```bash
git clone <repo-url> && cd go2serve
make build
```

## 2. Configure

Edit `docker-compose.yml` and replace `/path/to/files` with the directory you want to serve:

```yaml
volumes:
  - /home/your/website:/srv:ro
```

## 3. Run

```bash
make up
```

Your files are now served at `http://localhost`.

## 4. Enable HTTPS (optional)

### Let's Encrypt (automatic)

Your server must be reachable on port 80 from the internet, with DNS pointing to it.

Edit `docker-compose.yml` and uncomment the HTTPS port and Let's Encrypt command:

```yaml
ports:
  - "80:8080"
  - "443:8443"
volumes:
  - /home/your/website:/srv:ro
  - /path/to/certs:/certs
command: ["--root", "/srv", "--domain", "example.com"]
```

Replace `example.com` with your domain and `/path/to/certs` with a writable directory for certificate storage. Certificates are obtained on first connection and renewed automatically.

### Manual certificates

If you already have a certificate and key (e.g. from a corporate CA):

```yaml
ports:
  - "80:8080"
  - "443:8443"
volumes:
  - /home/your/website:/srv:ro
  - /path/to/certs:/certs:ro
command: ["--root", "/srv", "--cert", "/certs/cert.pem", "--key", "/certs/key.pem"]
```

Certificates are re-read from disk every 60 seconds, so rotation requires no restart.

## Other commands

| Command | What it does |
|---------|--------------|
| `make down` | Stop and remove the container |
| `make restart` | Restart the container |
| `make logs` | Tail container logs |
| `make status` | Show container status |
