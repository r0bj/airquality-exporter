package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/ryszard/sds011/go/sds011"
	flag "github.com/spf13/pflag"
)

const (
	ver string = "0.27"
	// 0 retries, exit on failure
	retries        int = 0
	apiCallTimeout int = 10
)

var (
	listenAddress = flag.String("web.listen-address", ":8080", "Address to listen on for web interface and telemetry")
	configFile    = flag.String("config-file", "config.ini", "Config file location")
	portPath      = flag.String("port-path", "/dev/ttyUSB0", "Serial port path")
	cycle         = flag.Int("cycle", 5, "Sensor cycle length in minutes")
	forceSetCycle = flag.Bool("force-set-cycle", true, "Force set cycle on every program start")
	verbose       = flag.Bool("verbose", false, "Enable verbose output")
)

var (
	airqualityPM = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "airquality_pm",
		Help: "Airquality PM metric",
	},
		[]string{"type"})
)

func sensorMakePassive(sensor *sds011.Sensor) error {
	var responseError error

	response := make(chan error)
Loop:
	for retry := 0; retry <= retries; retry++ {
		if retry > 0 {
			slog.Debug("Retrying API call", "retry", retry)
			time.Sleep(time.Second * time.Duration(retry))
		}

		go func() {
			if err := sensor.MakePassive(); err == nil {
				response <- nil
			} else {
				slog.Warn("Cannot switch sensor to passive mode", "error", err)
				response <- fmt.Errorf("Cannot switch sensor to passive mode: %v", err)
			}
		}()

		select {
		case err := <-response:
			if err == nil {
				responseError = nil
				break Loop
			} else {
				responseError = err
				continue Loop
			}
		case <-time.After(time.Second * time.Duration(apiCallTimeout)):
			slog.Warn("Device API response timeout", "retries", retry)
			responseError = fmt.Errorf("Device API response timeout (%d retries)", retry)
			continue Loop
		}
	}

	if responseError != nil {
		return responseError
	}

	return nil
}

func recordMetrics() {
	sensor, err := sds011.New(*portPath)
	if err != nil {
		slog.Error("Cannot create sensor instance", "error", err)
		os.Exit(1)
	}
	defer sensor.Close()

	if err := sensorMakePassive(sensor); err != nil {
		slog.Error("Cannot switch sensor to passive mode", "error", err)
		os.Exit(1)
	}

	if *forceSetCycle {
		slog.Info("Setting sensor cycle", "minutes", *cycle)
		if err := sensor.SetCycle(uint8(*cycle)); err != nil {
			slog.Error("Cannot set current cycle", "error", err)
			os.Exit(1)
		}
	} else {
		currentCycle, err := sensor.Cycle()
		if err != nil {
			slog.Error("Cannot get current cycle", "error", err)
			os.Exit(1)
		}
		if currentCycle != uint8(*cycle) {
			slog.Info("Setting sensor cycle", "minutes", *cycle)
			if err := sensor.SetCycle(uint8(*cycle)); err != nil {
				slog.Error("Cannot set current cycle", "error", err)
				os.Exit(1)
			}
		}
	}

	slog.Info("Switching sensor to active mode")
	if err := sensor.MakeActive(); err != nil {
		slog.Error("Cannot switch sensor to active mode", "error", err)
		os.Exit(1)
	}

	for {
		point, err := sensor.Get()
		if err != nil {
			slog.Error("Getting sensor measurement error", "error", err)
			continue
		}

		slog.Info("Sensor measurement results", "data", point)
		airqualityPM.WithLabelValues("pm2.5").Set(point.PM25)
		airqualityPM.WithLabelValues("pm10").Set(point.PM10)
	}
}

func main() {
	var loggingLevel = new(slog.LevelVar)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: loggingLevel}))
	slog.SetDefault(logger)

	flag.Parse()

	if *verbose {
		loggingLevel.Set(slog.LevelDebug)
		slog.Debug("Debug logging enabled")
	}

	slog.Info("Starting", "version", ver)

	go recordMetrics()

	slog.Info("Starting HTTP server", "address", *listenAddress)
	http.Handle("/metrics", promhttp.Handler())
	if err := http.ListenAndServe(*listenAddress, nil); err != nil {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}
}
