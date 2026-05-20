# Quickstart

Serve a directory over HTTP in three steps.

## 1. Clone

```bash
git clone https://github.com/crushdepth/go2serve.git && cd go2serve
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

Your files are now being served on port 80. Verify by visiting the appropriate address for your setup — `http://localhost`, your machine's IP, or your domain name.

## 4. Enable HTTPS (optional)

### Let's Encrypt (automatic)

Your server must be reachable on port 80 from the internet, with DNS pointing to it.

Edit `docker-compose.yml` and uncomment the HTTPS port, Let's Encrypt volume, and command:

```yaml
ports:
  - "80:8080"
  - "443:8443"
volumes:
  - /home/your/website:/srv:ro
  - go2serve-certs:/certs
command: ["--root", "/srv", "--domain", "example.com"]
```

And uncomment the named volume at the bottom of the file:

```yaml
volumes:
  go2serve-certs:
```

Replace `example.com` with your domain. Certificate storage is handled automatically by Docker — no directory setup needed. Certificates are obtained on first connection and renewed automatically.

**Note:** `make down` is safe — it preserves the certificate volume. Avoid `docker compose down -v` or `docker volume prune`, which will delete stored certificates. If lost, certificates are re-obtained automatically, but Let's Encrypt rate limits apply (5 per domain per week).

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
