# PM2 Manager API (Go)

Backend API untuk mengelola:
- **PM2 processes** & **Docker containers** (start/stop/restart/list)
- **File manager** per-app (list/read/write/delete/create dir) — support Docker & PM2
- **Token-based access** untuk user dengan path terbatas
- **Web Terminal** (NEW) — terminal interaktif di-root path user via WebSocket

## Stack

- Go 1.22+
- Echo (HTTP framework)
- MySQL (database)
- Gorilla WebSocket (terminal streaming)

## Konsep Path (mirip DirectAdmin)

Setiap `Token` punya `allowed_apps` (array nama app). Setiap `App` punya `cwd`
yang jadi **root path** untuk akses file & terminal user tersebut.

- User login dengan `accessCode` (token) → dapat JWT berisi `allowedApps`
- File manager & terminal **selalu di-scope ke `cwd` app** dalam `allowedApps`
- Path traversal (`..`, symlink keluar) di-block di server

## Terminal Sandbox

- Free-form command, tapi `cwd` dipaksa ke root path user
- Validasi path prefix di server sebelum & sesudah exec
- No `exec`, no `&`, no `nohup`, no shell built-in berbahaya
- Output stream real-time via WebSocket

## Setup

```bash
# 1. Copy env
cp .env.example .env
# edit DB & JWT secret

# 2. Install deps
go mod tidy

# 3. Migrate
go run ./cmd/migrate

# 4. Seed (opsional)
go run ./cmd/seed

# 5. Run
go run ./cmd/server
```

## API Endpoints

### Auth
- `POST /api/auth/login` — login (admin: username+password | user: accessCode)
- `GET  /api/auth/me`    — info user saat ini

### Apps
- `GET  /api/apps`               — list semua apps (filtered by allowed)
- `POST /api/apps/action`        — start/stop/restart

### File Manager
- `GET  /api/files?appName=&dir=`         — list files
- `POST /api/files/read`                  — body: `{appName, filePath}`
- `POST /api/files/write`                 — body: `{appName, filePath, content}`
- `POST /api/files/delete`                — body: `{appName, filePath}`
- `POST /api/files/create-dir`            — body: `{appName, dirPath}`

### Token (admin only)
- `GET    /api/tokens`
- `POST   /api/tokens`                    — body: `{label, allowedApps}`
- `PUT    /api/tokens/:id`
- `DELETE /api/tokens/:id`

### Terminal (WebSocket)
- `GET /api/terminal?appName=&token=<jwt>` — WebSocket upgrade

## Modul Panel v5 (cPanel-style)

Semua endpoint di bawah **khusus superadmin** (kecuali disebut lain).

### Terminal Root (PTY interaktif, setara SSH)
Shell login sungguhan milik user service (mis. `lpmobe`) di pseudo-terminal —
`vim`, `htop`, `sudo`, tab-completion jalan. Output disimpan di ring buffer
1 MiB per sesi berbasis offset byte, jadi reload halaman / koneksi putus tidak
kehilangan layar. Environment shell **bersih** (tidak mewarisi `DB_PASSWORD`,
`JWT_SECRET`, dsb dari service).

- `GET    /api/pty` — daftar sesi
- `POST   /api/pty` — buka sesi `{cols, rows, cwd}`
- `DELETE /api/pty/:id` — tutup sesi (SIGHUP ke process group, lalu SIGKILL)
- `POST   /api/pty/:id/input` — `{data}` ketikan
- `POST   /api/pty/:id/resize` — `{cols, rows}`
- `GET    /api/pty/:id/stream?offset=N` — output via SSE (lolos Cloudflare)
- `POST   /api/pty/:id/ticket` → `GET /api/pty/:id/ws?ticket=…&offset=N` — WebSocket (jalur cepat)

Sesi tanpa penonton dimatikan setelah 30 menit; maksimal 12 sesi hidup. Sesi
hilang bila service di-restart.

### Websites & File Manager (`WWW_ROOT`, default `/var/www`)
Semua path relatif terhadap root (`/simobe-lpm/app`). Path di-resolve termasuk
symlink — apa pun yang mengarah ke luar root ditolak (zip-slip juga dicegah).

- `GET  /api/www/sites` — deteksi aplikasi (Laravel, CI3/CI4, WordPress, PHP native, Node, statis…) + domain, SSL & PHP-FPM dari `NGINX_SITES_DIR`
- `GET  /api/www/list?path=` · `GET /api/www/stat?path=` · `GET /api/www/read?path=`
- `POST /api/www/write` `{path, content, mtime}` — `mtime` (epoch ms) mencegah menimpa perubahan orang lain (409)
- `POST /api/www/create|rename|delete|chmod|copy|move|compress|extract`
- `POST /api/www/upload` — multipart bertahap: `path, uploadId, offset, final, overwrite, chunk`
- `GET  /api/www/search?path=&q=` · `GET /api/www/du?path=`
- `POST /api/www/ticket {path, inline}` → `GET /api/www/download?ticket=…` (tiket 2 menit; folder diunduh sebagai zip)

Operasi berat (salin, pindah, kompres, ekstrak) tetap jalan walau klien putus;
bila > 80 detik dibalas `202` dan selesai di latar belakang.

### Docker & PM2 & Sistem
- `GET  /api/docker/overview` — container (+CPU/memori), image, info engine
- `GET  /api/docker/containers/:id/logs?tail=&timestamps=1` · `GET …/inspect`
- `POST /api/docker/containers/:id/action` — `start|stop|restart|pause|unpause|kill`
- `GET  /api/pm2/processes` · `GET /api/pm2/processes/:name/logs?lines=`
- `POST /api/pm2/processes/:name/action` — `start|stop|restart|reload|flush|reset`
- `POST /api/pm2/save`
- `GET  /api/system/overview` — host, layanan systemd, versi software, jaringan, disk

### Env baru
| Var | Default | Keterangan |
|---|---|---|
| `BIND_ADDR` | *(kosong = semua interface)* | set `127.0.0.1` agar API hanya lewat Nginx |
| `WWW_ROOT` | `/var/www` | root File Manager & Websites |
| `NGINX_SITES_DIR` | `/etc/nginx/sites-enabled` | sumber domain/SSL/PHP-FPM |
| `PM2_HOME` | `/root/.pm2` | **harus** home PM2 milik user service, mis. `/home/lpmobe/.pm2` |

### Test
```bash
go test ./internal/...
```

## Deploy

Lihat `DEPLOY.md`. Backend ini di-deploy **di host yang sama** dengan PM2 &
Docker daemon (butuh akses `/var/run/docker.sock` & binary `pm2` di PATH).
