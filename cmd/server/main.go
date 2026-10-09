package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/config"
	"github.com/tragasolusi/pm2-manager-api/internal/database"
	"github.com/tragasolusi/pm2-manager-api/internal/docker"
	"github.com/tragasolusi/pm2-manager-api/internal/pm2"
	"github.com/tragasolusi/pm2-manager-api/internal/repository"
	"github.com/tragasolusi/pm2-manager-api/internal/service"
	httpx "github.com/tragasolusi/pm2-manager-api/internal/transport/http"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

func main() {
	cfg := config.Load()

	if cfg.JWT.Secret == "" {
		log.Fatal("JWT_SECRET harus di-set di .env")
	}

	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatalf("database connect: %v", err)
	}
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	appRepo := repository.NewAppRepository(db)

	jwtSvc := auth.NewService(cfg.JWT.Secret, cfg.JWT.ExpiresIn)
	authSvc := service.NewAuthService(userRepo, tokenRepo, jwtSvc, cfg)

	var dockerCli *docker.Client
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		dockerCli, err = docker.New("/var/run/docker.sock")
		if err != nil {
			log.Printf("docker client: %v (PM2 only mode)", err)
			dockerCli = nil
		}
	} else {
		log.Println("/var/run/docker.sock tidak ditemukan, mode PM2 only")
	}

	pm2Cli := pm2.NewClient(cfg.PM2.Bin, cfg.PM2.Home)

	appSvc := service.NewAppService(dockerCli, pm2Cli, appRepo)
	fileSvc := service.NewFileService(dockerCli, pm2Cli)
	termSvc := service.NewTerminalService(dockerCli, pm2Cli)
	gitSvc := service.NewGitService(termSvc, pm2Cli)
	tokenSvc := service.NewTokenService(tokenRepo)
	statsSvc := service.NewStatsService()
	envSvc := service.NewEnvService(dockerCli, pm2Cli)
	logSvc := service.NewLogService(dockerCli, pm2Cli)
	cronSvc := service.NewCronService()
	dbSvc := service.NewDatabaseService(db.DB, cfg)
	sslSvc := service.NewSSLService()
	settingsSvc := service.NewSettingsService(userRepo, jwtSvc)
	backupSvc := service.NewBackupService()
	uptimeSvc := service.NewUptimeService()
	auditRepo := repository.NewAuditRepository(db)
	auditSvc := service.NewAuditService(auditRepo)
	aiConfigRepo := repository.NewAiConfigRepository(db)
	aiSettingsSvc := service.NewAiSettingsService(aiConfigRepo)

	// Ensure audit_logs table exists (best-effort, log warning on failure)
	if err := auditRepo.EnsureTable(context.Background()); err != nil {
		log.Printf("WARNING: ensure audit_logs: %v", err)
	}

	// Ensure ai_config table exists (best-effort, log warning on failure)
	if err := aiConfigRepo.EnsureTable(context.Background()); err != nil {
		log.Printf("WARNING: ensure ai_config: %v", err)
	}

	authH := httpx.NewAuthHandler(authSvc)
	appH := httpx.NewAppHandler(appSvc)
	fileH := httpx.NewFileHandler(fileSvc)
	tokenH := httpx.NewTokenHandler(tokenSvc)
	termH := httpx.NewTerminalHandler(jwtSvc, termSvc)
	gitH := httpx.NewGitHandler(gitSvc)
	statsH := httpx.NewStatsHandler(statsSvc)
	envH := httpx.NewEnvHandler(envSvc)
	logH := httpx.NewLogHandler(logSvc)
	cronH := httpx.NewCronHandler(cronSvc)
	dbH := httpx.NewDatabaseHandler(dbSvc)
	sslH := httpx.NewSSLHandler(sslSvc)
	settingsH := httpx.NewSettingsHandler(settingsSvc)
	backupH := httpx.NewBackupHandler(backupSvc)
	uptimeH := httpx.NewUptimeHandler(uptimeSvc)
	auditH := httpx.NewAuditHandler(auditSvc)
	aiSettingsH := httpx.NewAiSettingsHandler(aiSettingsSvc)
	httpx.SetAuditRecorder(auditSvc.Record)

	e := echo.New()
	e.HideBanner = true
	e.Use(echomw.Recover())
	e.Use(echomw.Logger())
	e.Use(echomw.CORSWithConfig(echomw.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAuthorization},
	}))

	// Mount routes at both "/" and "/panelPm/backend" so Nginx reverse-proxy
	// works whether the prefix is stripped upstream or not.
	register := func(prefix string) {
		g := e.Group(prefix)

		g.GET("/health", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]any{
				"status":    "ok",
				"timestamp": time.Now().UTC().Format(time.RFC3339),
			})
		})

		api := g.Group("/api")
		api.POST("/auth/login", authH.Login)

		a := api.Group("", httpx.Authenticate(jwtSvc))
		a.GET("/auth/me", authH.Me)

		a.GET("/apps", appH.Index)
		a.POST("/apps/action", appH.Action)
		a.GET("/server/stats", statsH.Stats)

		a.GET("/git/status", gitH.Status)
		a.GET("/git/branches", gitH.Branches)
		a.GET("/git/diff", gitH.Diff)
		a.POST("/git/pull", gitH.Pull)

		a.GET("/files", fileH.List)
		a.POST("/files/read", fileH.Read)
		a.POST("/files/write", fileH.Write)
		a.POST("/files/delete", fileH.Delete)
		a.POST("/files/create-dir", fileH.CreateDir)
		a.POST("/files/rename", fileH.Rename)
		a.POST("/files/upload", fileH.Upload)

		// Logs / Cron / Database / SSL / Settings / Env — previously 404
		a.GET("/logs", logH.Index)
		a.GET("/logs/stats", logH.Stats)
		a.GET("/cron", cronH.Index)
		a.POST("/cron", cronH.Store)
		a.PUT("/cron/:id", cronH.Update)
		a.DELETE("/cron/:id", cronH.Destroy)
		a.POST("/cron/:id/toggle", cronH.Toggle)
		a.GET("/databases", dbH.Index)
		a.GET("/databases/:dbName/tables", dbH.Tables)
		a.GET("/databases/:dbName/tables/:table/columns", dbH.Columns)
		a.GET("/databases/:dbName/tables/:table/data", dbH.Data)
		a.POST("/databases/:dbName/query", dbH.Query)
		a.GET("/ssl/certificates", sslH.Index)
		a.GET("/ssl/certificates/:id", sslH.Show)
		a.POST("/ssl/certificates", sslH.Store)
		a.DELETE("/ssl/certificates/:id", sslH.Destroy)
		a.POST("/ssl/letsencrypt", sslH.LetsEncrypt)
		a.GET("/settings", settingsH.Index)
		a.PUT("/settings", settingsH.Update)
		a.POST("/settings/password", settingsH.ChangePassword)
		a.GET("/env/:appName", envH.Index)
		a.PUT("/env/:appName", envH.Update)
		a.GET("/backups", backupH.Index)
		a.GET("/backups/log", backupH.Log)
		a.GET("/uptime", uptimeH.Index)
		a.GET("/uptime/:domain", uptimeH.Show)
		a.GET("/audit", auditH.Index)
		a.GET("/audit/live", auditH.Live)
		a.GET("/audit/stats", auditH.Stats)

		admin := a.Group("", httpx.RequireRole("superadmin"))
		admin.GET("/tokens", tokenH.Index)
		admin.POST("/tokens", tokenH.Store)
		admin.PUT("/tokens/:id", tokenH.Update)
		admin.DELETE("/tokens/:id", tokenH.Destroy)

		// Pengaturan AI + playground (khusus superadmin).
		admin.GET("/ai/settings", aiSettingsH.Status)
		admin.POST("/ai/models", aiSettingsH.FetchModels)
		admin.POST("/ai/settings", aiSettingsH.Save)
		admin.POST("/ai/cek-koneksi", aiSettingsH.CekKoneksi)
		admin.POST("/ai/playground", aiSettingsH.Playground)

		api.GET("/terminal", termH.Handle)
	}
	register("")
	register("/panelPm/backend")

	e.HTTPErrorHandler = func(err error, c echo.Context) {
		if c.Response().Committed {
			return
		}
		if he, ok := err.(*echo.HTTPError); ok {
			_ = response.Error(c, he.Code, strings.TrimSpace(he.Message.(string)))
			return
		}
		log.Printf("unhandled error: %v", err)
		_ = response.ServerError(c, "terjadi kesalahan server")
	}
	e.RouteNotFound("/*", func(c echo.Context) error {
		return response.NotFound(c, "endpoint tidak ditemukan")
	})

	go func() {
		log.Printf("Server berjalan di port %s", cfg.Port)
		log.Printf("Health (Lokal):    http://localhost:%s/health", cfg.Port)
		log.Printf("Health (Public):   https://sim-obe.radenfatah.ac.id/panelPm/backend/health")
		if err := e.Start(":" + cfg.Port); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("Shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = e.Shutdown(ctx)
}