package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/pkg/errors"
	"github.com/rancher/go-rancher-metadata/metadata"
	"github.com/rancher/log"
	"github.com/rancher/log/server"
	"github.com/rancher/per-host-subnet/hostnat"
	"github.com/rancher/per-host-subnet/hostports"
	"github.com/rancher/per-host-subnet/register"
	"github.com/rancher/per-host-subnet/routeupdate"
	"github.com/rancher/per-host-subnet/setting"
	"github.com/urfave/cli"
)

var VERSION = "v0.0.0-dev"

func main() {
	app := cli.NewApp()
	app.Name = "per-host-subnet"
	app.Version = VERSION
	app.Usage = "Support per-host-subnet networking"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:   "debug, d",
			EnvVar: "RANCHER_DEBUG",
		},
		cli.StringFlag{
			Name:   "metadata-address",
			Value:  setting.DefaultMetadataAddress,
			EnvVar: "RANCHER_METADATA_ADDRESS",
		},
		cli.BoolFlag{
			Name:   "enable-route-update",
			EnvVar: "RANCHER_ENABLE_ROUTE_UPDATE",
		},
		cli.StringFlag{
			Name:   "route-update-provider",
			Value:  setting.DefaultRouteUpdateProvider,
			EnvVar: "RANCHER_ROUTE_UPDATE_PROVIDER",
		},
		cli.BoolFlag{
			Name:  "register-service",
			Usage: "Register windows service, invalid for non windows OS.",
		},
		cli.BoolFlag{
			Name:  "unregister-service",
			Usage: "Unregister windows service, invalid for non windows OS.",
		},
		cli.IntFlag{
			Name:  "health-check-port",
			Usage: "Port to listen on for healthchecks",
			Value: 30088,
		},
	}
	app.Action = appMain
	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func appMain(ctx *cli.Context) error {
	server.StartServerWithDefaults()

	if ctx.Bool("debug") {
		log.SetLevelString("debug")
	}

	if ctx.Bool("register-service") && ctx.Bool("unregister-service") {
		log.Fatal("Can not use flag register-service and unregister-service at the same time")
	}
	if err := register.Init(ctx.Bool("register-service"), ctx.Bool("unregister-service")); err != nil {
		return err
	}

	done := make(chan error)

	m, err := metadata.NewClientAndWait(fmt.Sprintf(setting.MetadataURL, ctx.String("metadata-address")))
	if err != nil {
		return errors.Wrap(err, "Failed to create metadata client")
	}

	if ctx.Bool("enable-route-update") {
		_, err := routeupdate.Run(ctx.String("route-update-provider"), m)
		if err != nil {
			return err
		}
	}

	err = hostnat.Watch(m)
	if err != nil {
		return err
	}

	err = hostports.Watch(m)
	if err != nil {
		return err
	}

	go func(exit chan<- error) {
		err := startHealthCheck(ctx.Int("health-check-port"), m)
		exit <- errors.Wrapf(err, "Healthcheck provider died.")
	}(done)

	err = <-done
	log.Errorf("Exiting per-host-subnet with error: %v", err)
	return <-done
}

func startHealthCheck(listen int, md metadata.Client) error {
	http.HandleFunc("/healthcheck", func(w http.ResponseWriter, r *http.Request) {
		var errMsg string
		healthy := true
		_, err := md.GetVersion()
		if err != nil {
			healthy = false
			errMsg = fmt.Sprintf("error fetching metadata version: %v", err)
		}
		if healthy {
			fmt.Fprint(w, "ok")
		} else {
			log.Errorf("failed healtcheck: %v", errMsg)
			http.Error(w, "Metadata and dns is unreachable", http.StatusNotFound)
		}
	})
	log.Infof("Listening for health checks on 0.0.0.0:%d/healthcheck", listen)
	err := http.ListenAndServe(fmt.Sprintf(":%d", listen), nil)
	return err
}
