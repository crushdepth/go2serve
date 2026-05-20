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

## Other commands

| Command | What it does |
|---------|--------------|
| `make down` | Stop and remove the container |
| `make restart` | Restart the container |
| `make logs` | Tail container logs |
| `make status` | Show container status |
