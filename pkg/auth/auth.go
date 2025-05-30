package auth

import (
	"github.com/DIMO-Network/credit-tracker/internal/config"
	jwtware "github.com/gofiber/contrib/jwt"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

const (
	ContextKey = "user"
)

// Token is the token for the user.
type Token struct {
	jwt.RegisteredClaims
	CustomDexClaims
}

// CustomDexClaims is the custom claims for the token.
type CustomDexClaims struct {
	ProviderID      string `json:"provider_id"`
	AtHash          string `json:"at_hash"`
	EmailVerified   bool   `json:"email_verified"`
	EthereumAddress string `json:"ethereum_address"`
}

// Middleware is the middleware for Dex JWT authentication.
func Middleware(settings *config.Settings) fiber.Handler {
	return jwtware.New(jwtware.Config{
		JWKSetURLs: []string{settings.JWKKeySetURL},
		Claims:     &Token{},
		ContextKey: ContextKey,
	})
}

// GetDexJWT returns the dex jwt from the context.
func GetDexJWT(c *fiber.Ctx) (*Token, bool) {
	localValue := c.Locals(ContextKey)
	if localValue == nil {
		return nil, false
	}
	user, ok := localValue.(*Token)
	return user, ok
}
