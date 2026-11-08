// terraform-provider-gyro: Terraform provider for Gyro (gyrohosting.com) cloud servers.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

// version is set by goreleaser (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for debuggers like delve")
	flag.Parse()
	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/gyro-hosting/gyro",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
