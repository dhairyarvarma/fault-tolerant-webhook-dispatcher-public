package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/targetsim"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	addr := os.Getenv("TARGETSIM_ADDR")
	if addr == "" {
		addr = ":9090"
	}

	logger.Info("targetsim listening", "addr", addr)
	if err := http.ListenAndServe(addr, targetsim.NewHandler()); err != nil {
		logger.Error("targetsim exited with error", "error", err)
		os.Exit(1)
	}
}
