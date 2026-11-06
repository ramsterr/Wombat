package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/serve"
)

// cmdServe runs the phone web client. Each browser connection gets its own
// `wombat bridge` process, so the agent behaves exactly as it does in the TUI.
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	host := fs.String("host", "", "address to listen on (default 127.0.0.1)")
	lan := fs.Bool("lan", false, "listen on all interfaces so a phone on your network can connect")
	port := fs.Int("port", serve.DefaultPort, "port to listen on")
	token := fs.String("token", "", "access token (default: a new random token each run)")
	dir := fs.String("dir", "", "project directory the agent works in (default: current directory)")
	bridge := fs.String("bridge", "", "wombat executable to run as the bridge (default: this executable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument: %s", fs.Arg(0))
	}
	if *port < 1 || *port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", *port)
	}

	listenHost := *host
	if *lan {
		if listenHost != "" && listenHost != serve.LANHost {
			return fmt.Errorf("--lan and --host %s conflict; use one or the other", listenHost)
		}
		listenHost = serve.LANHost
	}
	if listenHost == "" {
		listenHost = serve.DefaultHost
	}

	projectDir := *dir
	if projectDir != "" {
		abs, err := filepath.Abs(projectDir)
		if err != nil {
			return err
		}
		st, err := os.Stat(abs)
		if err != nil {
			return err
		}
		if !st.IsDir() {
			return fmt.Errorf("%s is not a directory", abs)
		}
		projectDir = abs
	}

	logPath := filepath.Join(config.Home(), "serve.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}

	srv, err := serve.New(serve.Options{
		Host:    listenHost,
		Port:    *port,
		Token:   strings.TrimSpace(*token),
		Dir:     projectDir,
		Exe:     *bridge,
		LogFile: logPath,
	})
	if err != nil {
		return err
	}
	opts := srv.Options()
	for _, line := range serve.Banner(opts) {
		fmt.Println(line)
	}
	fmt.Println(dim("bridge logs: " + logPath))
	fmt.Println(dim("ctrl+c to stop"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.ListenAndServe(ctx)
}
