package management

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"golang.org/x/crypto/bcrypt"
)

func BenchmarkAuthenticateManagementKeyValidRemote(b *testing.B) {
	secret := "benchmark-management-secret"
	secretHash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		b.Fatal(err)
	}
	h := &Handler{
		cfg: &config.Config{RemoteManagement: config.RemoteManagement{
			AllowRemote: true,
			SecretKey:   string(secretHash),
		}},
		failedAttempts: make(map[string]*attemptInfo),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		allowed, statusCode, errMessage := h.AuthenticateManagementKey("203.0.113.10", false, secret)
		if !allowed || statusCode != 0 || errMessage != "" {
			b.Fatalf("authentication failed: allowed=%v status=%d error=%q", allowed, statusCode, errMessage)
		}
	}
}
