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
	tokenSvc := service.NewTokenService(tokenRepo)

	authH := httpx.NewAuthHandler(authSvc)
	appH := httpx.NewAppHandler(appSvc)
	fileH := httpx.NewFileHandler(fileSvc)
	tokenH := httpx.NewTokenHandler(tokenSvc)
	termH := httpx.NewTerminalHandler(jwtSvc, termSvc)

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

		a.GET("/files", fileH.List)
		a.POST("/files/read", fileH.Read)
		a.POST("/files/write", fileH.Write)
		a.POST("/files/delete", fileH.Delete)
		a.POST("/files/create-dir", fileH.CreateDir)

		admin := a.Group("", httpx.RequireRole("superadmin"))
		admin.GET("/tokens", tokenH.Index)
		admin.POST("/tokens", tokenH.Store)
		admin.PUT("/tokens/:id", tokenH.Update)
		admin.DELETE("/tokens/:id", tokenH.Destroy)

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