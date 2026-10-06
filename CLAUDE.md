# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## High-Level Summary
Go-based **Prometheus exporter** for theoldschool.cc (UNIT3D tracker). Gin for routing, Prometheus client for exposition. Session login with CSRF form, then HTML scraping of the top-nav ratio bar. **No Cloudflare challenge** for plain HTTP with a browser User-Agent, so no FlareSolverr.

## Key Components
- `TheOldSchoolClient` (main.go): cookie jar, `Login()`, `FetchMetrics()` (re-logins once when redirected to `/login`).
- Login: `GET /login`, copy all hidden inputs of `form.auth-form__form` (`_token`, `_captcha`, timestamp honeypot field with random name), keep `_username` empty, wait 3 s, `POST /login` with `username`/`password`.
- Metrics: `GET /` then regex on `li.ratio-bar__uploaded` and `li.ratio-bar__downloaded` (text like `511.96 GiB`), converted with binary units (1024).
- `/metrics` fetches fresh values on each scrape; `/health` returns status + authenticated flag.

## Configuration (.env)
```
THEOLDSCHOOL_BASE_URL   # default https://theoldschool.cc
THEOLDSCHOOL_USERNAME
THEOLDSCHOOL_PASSWORD
PORT                    # default 9090, dev uses 9103
METRICS_PATH            # default /metrics
SCRAPE_INTERVAL         # e.g. 5m
```

## Metrics
| Metric | Type |
|--------|------|
| `theoldschool_total_uploaded_bytes` | Gauge |
| `theoldschool_total_downloaded_bytes` | Gauge |

## Commands
```bash
go build ./... && go vet ./... && go test ./...
docker compose -f docker-compose.dev.yml up --build
curl http://localhost:9103/metrics
```

## Notes
- Very similar to the gemini-tracker exporter (same UNIT3D markup).
- Precision limited by the 2-decimal display (no exact byte count exposed). UNIT3D API tokens do not expose user stats, so session login is used.
