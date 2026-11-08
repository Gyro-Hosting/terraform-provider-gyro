// gyroctl: minimal command line client for the Gyro Public API v1.
//
//	export GYRO_TOKEN=wck_...            # client API key
//	gyroctl products [--location fsn1]   gyroctl images | locations | balance | invoices
//	gyroctl servers                      gyroctl server <id>
//	gyroctl action <id> <poweron|shutdown|reboot|poweroff|reset|rebuild|reset_password|enable_rescue|disable_rescue> [--image slug] [--confirm]
//	gyroctl ssh-keys                     gyroctl ssh-key-add <name> <public-key-file>   gyroctl ssh-key-rm <id>
//	gyroctl create --product <id> --image <slug> [--hostname h] [--ssh-key <id>] [--dry-run | --confirm]
//	gyroctl delete <id> --confirm
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
)

var version = "dev"

func die(err error) {
	fmt.Fprintln(os.Stderr, "gyroctl:", err)
	os.Exit(1)
}

func out(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func id(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		die(fmt.Errorf("invalid id %q", s))
	}
	return n
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		fmt.Println("gyroctl " + version + " - Gyro Public API client. Set GYRO_TOKEN (and optionally GYRO_ENDPOINT). See the package comment for commands.")
		return
	}
	tok := os.Getenv("GYRO_TOKEN")
	if tok == "" {
		die(fmt.Errorf("GYRO_TOKEN is not set"))
	}
	c := client.New(os.Getenv("GYRO_ENDPOINT"), tok, "gyroctl/"+version)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	loc := fs.String("location", "", "location filter")
	image := fs.String("image", "", "image slug")
	host := fs.String("hostname", "", "hostname")
	prod := fs.Int64("product", 0, "product id")
	key := fs.Int64("ssh-key", 0, "ssh key id")
	confirm := fs.Bool("confirm", false, "confirm a paid or destructive operation")
	dry := fs.Bool("dry-run", false, "price quote only")
	wait := fs.Bool("wait", false, "wait until the server is ready")
	pos := []string{}
	for len(args) > 0 {
		if strings.HasPrefix(args[0], "-") {
			_ = fs.Parse(args)
			args = fs.Args()
			continue
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
	need := func(n int) {
		if len(pos) < n {
			die(fmt.Errorf("%s: missing argument", cmd))
		}
	}
	var v any
	var err error
	switch cmd {
	case "products":
		v, err = c.Products(ctx, *loc, "")
	case "locations":
		v, err = c.Locations(ctx)
	case "images":
		v, err = c.Images(ctx)
	case "balance":
		v, err = c.Balance(ctx)
	case "invoices":
		v, err = c.Invoices(ctx)
	case "servers":
		v, err = c.Servers(ctx, "")
	case "server":
		need(1)
		v, err = c.Server(ctx, id(pos[0]))
	case "action":
		need(2)
		body := map[string]any{}
		if *image != "" {
			body["image"] = *image
		}
		if *confirm {
			body["confirm"] = true
		}
		v, err = c.Action(ctx, id(pos[0]), pos[1], body)
	case "ssh-keys":
		v, err = c.SSHKeys(ctx)
	case "ssh-key-add":
		need(2)
		b, rerr := os.ReadFile(pos[1])
		if rerr != nil {
			die(rerr)
		}
		v, err = c.CreateSSHKey(ctx, pos[0], strings.TrimSpace(string(b)))
	case "ssh-key-rm":
		need(1)
		err = c.DeleteSSHKey(ctx, id(pos[0]))
		v = map[string]any{"deleted": err == nil}
	case "create":
		req := client.CreateServerRequest{ProductID: *prod, Image: *image, Hostname: *host, DryRun: *dry, Confirm: *confirm}
		if *key > 0 {
			req.SSHKeys = []int64{*key}
		}
		var r *client.CreateServerResult
		r, err = c.CreateServer(ctx, req, client.NewIdempotencyKey())
		v = r
		if err == nil && *wait && r.Server != nil {
			v, err = c.WaitReady(ctx, r.Server.ID, "running", 5*time.Second)
		}
	case "delete":
		need(1)
		if !*confirm {
			die(fmt.Errorf("delete destroys the server and its data: add --confirm"))
		}
		err = c.DeleteServer(ctx, id(pos[0]), true)
		v = map[string]any{"cancellation_requested": err == nil}
	default:
		die(fmt.Errorf("unknown command %q", cmd))
	}
	if err != nil {
		die(err)
	}
	out(v)
}
