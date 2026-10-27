package middleware

import "github.com/regiellis/hyperchi/hyperchi"

// The rate limiter lives in package hyperchi. These aliases keep it reachable
// from this import path. The hyperchi docs describe their behavior.

// RateLimitConfig is hyperchi.RateLimitConfig.
type RateLimitConfig = hyperchi.RateLimitConfig

// ClientInfo is hyperchi.ClientInfo.
type ClientInfo = hyperchi.ClientInfo

// RateLimiter is hyperchi.RateLimiter.
type RateLimiter = hyperchi.RateLimiter

// EndpointRateLimiter is hyperchi.EndpointRateLimiter.
type EndpointRateLimiter = hyperchi.EndpointRateLimiter

// DefaultRateLimitConfig is hyperchi.DefaultRateLimitConfig.
var DefaultRateLimitConfig = hyperchi.DefaultRateLimitConfig

// NewRateLimiter is hyperchi.NewRateLimiter.
var NewRateLimiter = hyperchi.NewRateLimiter

// NewRateLimiterE is hyperchi.NewRateLimiterE.
var NewRateLimiterE = hyperchi.NewRateLimiterE

// RateLimit is hyperchi.RateLimit.
var RateLimit = hyperchi.RateLimit

// RateLimitWithConfig is hyperchi.RateLimitWithConfig.
var RateLimitWithConfig = hyperchi.RateLimitWithConfig

// NewEndpointRateLimiter is hyperchi.NewEndpointRateLimiter.
var NewEndpointRateLimiter = hyperchi.NewEndpointRateLimiter
