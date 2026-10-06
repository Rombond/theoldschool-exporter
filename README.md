# The Old School Exporter

A Go-based Prometheus exporter that reads your upload/download totals from [theoldschool.cc](https://theoldschool.cc) (UNIT3D tracker) and exposes them via the Prometheus HTTP interface.

- Logs in with your username/password (session cookie + CSRF form, no FlareSolverr)
- Reads the top-nav ratio bar of the home page (`Uploaded` / `Downloaded`)
- Exposes `/metrics` and `/health`

## Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `theoldschool_total_uploaded_bytes` | Gauge | Total uploaded bytes |
| `theoldschool_total_downloaded_bytes` | Gauge | Total downloaded bytes |

The site only displays sizes rounded to 2 decimals in binary units (e.g. `511.96 GiB`), so values are converted with 1024 multipliers and are approximate (about 10 MiB precision at GiB scale).

## Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `THEOLDSCHOOL_BASE_URL` | Site URL | `https://theoldschool.cc` |
| `THEOLDSCHOOL_USERNAME` | Login username | *required* |
| `THEOLDSCHOOL_PASSWORD` | Login password | *required* |
| `PORT` | Server listen port | `9090` (`9103` in `.env.example`) |
| `METRICS_PATH` | Metrics endpoint path | `/metrics` |
| `SCRAPE_INTERVAL` | Informational (data is fetched on each scrape) | `5m` |

```bash
cp .env.example .env   # then fill in username and password
```

## Run

```bash
go build -o theoldschool_exporter . && ./theoldschool_exporter
# or
docker compose -f docker-compose.dev.yml up --build
curl http://localhost:9103/metrics
curl http://localhost:9103/health
```

## Authentication Flow

1. `GET /login`, parse the form: `_token` (CSRF), `_captcha`, honeypot `_username` (left empty) and a randomly named timestamp field
2. Wait 3 s (anti-bot minimum time), then `POST /login` with `username`, `password` and the hidden fields
3. Keep the `the_old_school_session` / `XSRF-TOKEN` cookies in a cookie jar
4. `GET /` and parse `li.ratio-bar__uploaded` / `li.ratio-bar__downloaded`; if redirected to `/login`, log in again once

## Prometheus

```yaml
scrape_configs:
  - job_name: 'theoldschool_exporter'
    static_configs:
      - targets: ['localhost:9103']
```

## License

[MIT](./LICENSE)
