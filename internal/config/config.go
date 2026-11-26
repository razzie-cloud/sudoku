// Package config reads the service's operator configuration without logging secrets.
package config

import (
	"context"
	"errors"
	"flag"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/razzie-cloud/sudoku/internal/brokerdiscovery"
	"github.com/razzie-cloud/sudoku/internal/gatewayidentity"
)

type Config struct {
	Port, Origin, LandingOrigin, AccessGatewayURL string
	PostgresURI, PostgresURIFrom, PasswordFile    string
	BrokerTokenFile, BrokerCAFile, LogLevel       string
}

func Load(args []string, env func(string) string) (Config, error) {
	var c Config
	f := flag.NewFlagSet("sudoku", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	str := func(dest *string, name, variable, fallback string) {
		value := env(variable)
		if value == "" {
			value = fallback
		}
		f.StringVar(dest, name, value, variable)
	}
	str(&c.Port, "port", "SERVICE_PORT", "8080")
	str(&c.Origin, "origin", "ORIGIN", "")
	str(&c.LandingOrigin, "landing-origin", "LANDING_ORIGIN", "https://razzie.cloud")
	str(&c.AccessGatewayURL, "access-gateway-url", "ACCESS_GATEWAY_URL", "")
	str(&c.PostgresURI, "postgres-uri", "POSTGRES_URI", "")
	str(&c.PostgresURIFrom, "postgres-uri-from", "POSTGRES_URI_FROM", "")
	str(&c.PasswordFile, "postgres-password-file", "POSTGRES_PASSWORD_FILE", "")
	str(&c.BrokerTokenFile, "broker-token-file", "BROKER_TOKEN_FILE", "")
	str(&c.BrokerCAFile, "broker-ca-file", "BROKER_CA_FILE", "")
	str(&c.LogLevel, "log-level", "LOG_LEVEL", "INFO")
	if err := f.Parse(args); err != nil {
		return c, errors.New("invalid command-line configuration")
	}
	if f.NArg() != 0 {
		return c, errors.New("unexpected arguments")
	}
	p, err := strconv.Atoi(c.Port)
	if err != nil || p < 1 || p > 65535 {
		return c, errors.New("SERVICE_PORT must be between 1 and 65535")
	}
	for name, value := range map[string]string{"ORIGIN": c.Origin, "LANDING_ORIGIN": c.LandingOrigin} {
		if !validOrigin(value) {
			return c, errors.New(name + " must be an exact http(s) origin")
		}
	}
	if c.AccessGatewayURL == "" {
		return c, errors.New("ACCESS_GATEWAY_URL is required")
	}
	if err := gatewayidentity.ValidateURL(c.AccessGatewayURL); err != nil {
		return c, err
	}
	if c.PostgresURI == "" && c.PostgresURIFrom == "" {
		return c, errors.New("set POSTGRES_URI or POSTGRES_URI_FROM")
	}
	c.LogLevel = strings.ToUpper(c.LogLevel)
	switch c.LogLevel {
	case "DEBUG", "INFO", "WARN", "ERROR":
	default:
		return c, errors.New("LOG_LEVEL must be DEBUG, INFO, WARN, or ERROR")
	}
	return c, nil
}

func validOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" {
		return false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return !strings.ContainsAny(u.Hostname(), " \\%") && !strings.HasSuffix(u.Host, ":")
}

// DatabaseURI gives a direct URI precedence over discovery, including its
// credential files; direct operation never contacts the broker.
func (c Config) DatabaseURI(ctx context.Context) (string, error) {
	value := c.PostgresURI
	if value == "" {
		var err error
		value, err = brokerdiscovery.Fetch(ctx, c.PostgresURIFrom, brokerdiscovery.Options{TokenFile: c.BrokerTokenFile, CAFile: c.BrokerCAFile})
		if err != nil {
			return "", err
		}
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User == nil || u.User.Username() == "" || u.Path == "" || u.Path == "/" || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("invalid PostgreSQL URI")
	}
	if c.PasswordFile != "" {
		f, err := os.Open(c.PasswordFile)
		if err != nil {
			return "", errors.New("cannot read PostgreSQL password file")
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, 4097))
		if err != nil || len(b) > 4096 {
			return "", errors.New("invalid PostgreSQL password file")
		}
		u.User = url.UserPassword(u.User.Username(), strings.TrimRight(string(b), "\r\n"))
	}
	return u.String(), nil
}

func (c Config) Address() string { return net.JoinHostPort("", c.Port) }
