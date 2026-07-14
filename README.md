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

## Deploy

Lihat `DEPLOY.md`. Backend ini di-deploy **di host yang sama** dengan PM2 &
Docker daemon (butuh akses `/var/run/docker.sock` & binary `pm2` di PATH).
