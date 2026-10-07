# HyperChi Security Guide

## Overview

HyperChi has been designed with security as a first-class concern. This document outlines the security features implemented and best practices for deploying secure HyperChi applications.

## ✅ Implemented Security Features

### 1. Password Security
- **Bcrypt hashing** with cost factor 14 (excellent security)
- **Password strength validation** (weak/medium/strong/very strong)
- **Common password detection** to prevent weak passwords
- **Automatic password rehashing** when security standards are updated
- **Timing-safe password comparison** to prevent timing attacks

```go
// Secure password hashing
hash, err := security.HashPassword(password)
if err != nil {
    return fmt.Errorf("failed to hash password: %w", err)
}

// Secure password verification, inside a handler
if !security.CheckPasswordHash(password, hash) {
    return hyperchi.Unauthorized("Invalid credentials.")
}
```

### 2. CSRF Protection
- **Double-submit cookie pattern** for stateless protection: safe methods (GET, HEAD,
  OPTIONS, TRACE) set a `csrf_token` cookie, and other methods must send its token back
- **Masked per render**: `c.CSRFToken()` returns the token XORed with a fresh random pad, so
  no two pages carry the same string and a compressed page cannot leak it (BREACH). The
  form field accepts masked tokens only; the `X-CSRF-Token` header also accepts the raw
  cookie value for script that reads the cookie
- **Cryptographically secure token generation** (32 bytes)
- **Timing-safe token validation** to prevent timing attacks
- The token reaches handlers as `c.CSRFToken()`; send it back as the `csrf_token` form field
  or the `X-CSRF-Token` header (the header suits htmx requests)
- A missing or mismatched token is a 403

```go
// Enable CSRF protection, before registering routes
app.Use(app.Middleware.CSRF())

app.Get("/profile", func(c *hyperchi.Context) error {
    return c.View("profile", hyperchi.H{"csrf_token": c.CSRFToken()})
})
```

```html
<form method="post" action="/profile">
    <input type="hidden" name="csrf_token" value="{{ .csrf_token }}">
    <!-- form fields -->
</form>

<!-- or, for every htmx request issued inside <body>. htmx 4 inherits an
     attribute only when it is marked :inherited; a plain hx-headers here
     covers requests from <body> itself and no others. -->
<body hx-headers:inherited='{"X-CSRF-Token": "{{ .csrf_token }}"}'>
```

### 3. XSS Protection
- **Automatic HTML escaping** of all template variables
- **SafeHTML type** for trusted content that shouldn't be escaped
- **Template functions** for manual escaping, plus `ScrubHTMX` for third-party markup
- `SanitizeInput` and `ValidateUserInput` are denylists, not an XSS defense: they turn away
  obvious probes and nothing more. Rendering through `html/template` is the defense; for
  untrusted markup you keep, use an allowlist sanitizer such as bluemonday

```go
// Template data is automatically escaped
data := map[string]interface{}{
    "userInput": "<script>alert('xss')</script>", // Auto-escaped
    "safeHTML":  security.MarkSafe("<b>Bold</b>"), // Not escaped
}

```

```html
<!-- html/template escapes .userInput already; adding escape would double-escape it -->
<p>{{ .userInput }}</p>
<!-- safe bypasses escaping: only for markup the application built itself -->
<div>{{ .trustedContent | safe }}</div>
```

### 4. Rate Limiting
- **Token-bucket rate limiting** to prevent abuse; the request count and window set the
  refill rate
- **Per-client tracking** keyed by the client IP, which honors forwarding headers only from
  `Security.TrustedProxies`
- **Bounded memory**: at most `MaxKeys` buckets (default 10,000), with inactive buckets
  reclaimed when full and no cleanup goroutine
- **`Retry-After`** on every 429
- Limits are per process

```go
// 100 requests per minute per client
app.Use(app.Middleware.RateLimit(100, time.Minute))

// Explicit token-bucket settings
limiter, err := hyperchi.NewRateLimiterE(&hyperchi.RateLimitConfig{
    Burst:          20,
    RefillInterval: 3 * time.Second,
})
```

`docs/PRODUCTION.md` covers key functions, trusted proxies, and denial responses.

### 5. File Upload Security
- **Size limits** as one budget: `MaxFileSize` per file, `MaxTotalSize` for all files
  together (default `MaxFiles * MaxFileSize`), and the request body capped at `MaxTotalSize`
  plus 1 MiB. `UploadMiddleware` answers a size failure with 413 and any other failure,
  a malformed body included, with 400
- **MIME type validation** against allowed types whitelist
- **File extension validation** with secure whitelist approach
- **Content-type verification** to prevent MIME confusion attacks
- **Filename sanitization** to prevent path traversal vulnerabilities
- **Authentication requirements** enforced through the verified user in the request context
- **File count limits** per upload request
- **Content inspection** using first 512 bytes for accurate type detection

```go
// Secure file upload configuration
upload := &security.FileUploadConfig{
    MaxFileSize:    10 << 20, // 10MB
    AllowedTypes:   []string{"image/jpeg", "image/png", "application/pdf"},
    AllowedExtensions: []string{".jpg", ".jpeg", ".png", ".pdf"},
    MaxFiles:       5,
    RequireAuth:    true,
}

// authHelper is the AuthHelper the login Authenticator issues tokens from (see
// Authentication System). Per-route middleware runs in order: authenticate
// first, then validate the upload.
app.Post("/upload", uploadHandler,
    security.RequireAuth(authHelper),
    security.UploadMiddleware(upload),
)
```

With `RequireAuth` enabled, `ValidateUpload` requires the user identity set by
`security.RequireAuth` or trusted custom middleware using `security.WithUserID`.
A bearer token's format alone never authenticates an upload. Public upload endpoints
must explicitly set `RequireAuth: false`.

#### Storing uploads

Store files with `security.SaveUpload(dir, fileHeader, opts)` or, after
`UploadMiddleware`, `security.GetValidationResult(r).SaveAll(dir, opts)`. Never build a
path from the client's filename.

- The stored name is 128 random bits in hex plus an extension looked up from the type
  sniffed from the file's first 512 bytes, never from the client's filename or declared
  type. `SaveOptions.Extensions` is that allowlist (default `DefaultSaveExtensions()`:
  JPEG, PNG, GIF, WebP, plain text, PDF). An unlisted type is refused. HTML, XML, SVG,
  and JavaScript are refused even when listed.
- Files are written through an `os.Root` on `dir`, so nothing lands outside it, symlinks
  included. An existing name is never overwritten (`O_EXCL`).
- Files get mode `0640` and a missing `dir` is created `0750`.
- The copy streams and stops past `SaveOptions.MaxSize` (default 10 MiB for `SaveUpload`,
  the validator's `MaxFileSize` for `SaveAll`) with an `*UploadLimitError`; the partial
  file is removed. `SaveAll` keeps all files or none.
- `SavedFile.OriginalName` is the client's name, cleaned, for display only.

```go
app.Post("/upload", func(c *hyperchi.Context) error {
    saved, err := security.GetValidationResult(c.Request()).SaveAll("data/uploads", security.SaveOptions{})
    if err != nil {
        return hyperchi.BadRequest("Upload could not be stored").Wrap(err)
    }
    return c.JSON(saved)
}, security.RequireAuth(authHelper), security.UploadMiddleware(upload))
```

Serve stored files with a `Content-Type` derived from the stored extension and
`X-Content-Type-Options: nosniff`, preferably as `Content-Disposition: attachment`.

### 6. Input Validation System
- **Type-safe validation** with chainable rules
- **Email, phone, URL, UUID validation** with robust regex patterns
- **Form and JSON data validation** with unified interface
- **Custom error messages** and field-specific validation
- **HTMX-aware validation** with conditional response handling
- **Pre-built validation schemas** for common use cases

```go
// Input validation example: tags on the struct Bind fills
type Signup struct {
    Email    string `form:"email" validate:"required,email"`
    Password string `form:"password" validate:"required,min=8"`
    Name     string `form:"name" validate:"required,max=100"`
}

in, err := hyperchi.Bind[Signup](c)
if fields := hyperchi.FieldErrors(err); fields != nil {
    return c.Status(422).Fragment("form", hyperchi.H{"in": in, "errors": fields})
} else if err != nil {
    return err
}
```

### 7. Authentication System
- **Complete user management** with secure session handling
- **Login attempt tracking** with IP and User-Agent logging
- **Account status validation** (active users only)
- **Session token creation and validation** with configurable expiry
- **Password change validation** requiring current password verification
- **RequireAuth middleware** for protecting routes
- **Login attempt IPs** from the TCP peer; `X-Forwarded-For` (walked from the right) and
  `X-Real-IP` count only when the peer is a loopback reverse proxy

```go
// One AuthHelper issues tokens at login and validates them in RequireAuth.
// Give it a durable store with SetSessionStore before serving.
authHelper := security.NewAuthHelper()
auth := security.NewAuthenticator(userRepo, authHelper)

// Authentication middleware on the routes that need it
app.Group("/account", func(g *hyperchi.HyperChi) {
    g.Use(security.RequireAuth(authHelper))
    g.Get("/", account)
})

// Login: one generic message for every failure, so responses cannot enumerate accounts
app.Post("/login", func(c *hyperchi.Context) error {
    result := auth.AuthenticateUser(security.ExtractLoginAttempt(c.Request()))
    if !result.Success {
        return hyperchi.Unauthorized(result.Error)
    }
    // ... set the session cookie from result.Token
    return c.Redirect("/account")
})
```

### 8. Security Headers
`app.Middleware.Security()` sets, on every response:
- **X-Content-Type-Options: nosniff** to prevent MIME sniffing
- **X-Frame-Options: DENY** to prevent clickjacking
- **Referrer-Policy: strict-origin-when-cross-origin**

Outside development it also sets:
- **Content-Security-Policy** from `Security.CSPPolicy` (`CSP_POLICY`), with a default that
  allows the `'unsafe-eval'` HyperChi directives and htmx event handlers need
- **Strict-Transport-Security** from `Security.HSTS`, when set
- **Permissions-Policy** denying camera, microphone, and geolocation

## 🔒 Production Security Checklist

### Environment Configuration
- [ ] Set `ENVIRONMENT=production` and build the app from `config.FromEnv()`
- [ ] List your reverse proxy's CIDRs in `Security.TrustedProxies`, and no others
- [ ] Replace the `JWT_SECRET` and `SESSION_SECRET` placeholders that `hyperchi deploy init`
  writes; `hyperchi deploy production` warns when `.env.production` lacks `JWT_SECRET`
- [ ] Configure secure database credentials
- [ ] Enable HTTPS/TLS in production
- [ ] Set secure cookie attributes

### Password Security
- [ ] Enforce strong password requirements
- [ ] Use environment variables for admin credentials
- [ ] Never commit passwords to version control
- [ ] Implement password rotation policies

### Network Security
- [ ] Configure firewall rules
- [ ] Use reverse proxy (nginx/Apache) with security headers
- [ ] Enable rate limiting appropriate for your use case
- [ ] Configure CORS origins restrictively

### Database Security
- [ ] Use connection pooling with reasonable limits
- [ ] Enable database query logging in development only
- [ ] Use parameterized queries (already handled by HyperChi)
- [ ] Regular database backups with encryption

### Monitoring & Logging
- [ ] Configure structured logging
- [ ] Monitor for suspicious activity patterns
- [ ] Set up alerts for repeated failed login attempts
- [ ] Log security events (auth failures, rate limit hits)

## 🚨 Security Vulnerabilities That Were Fixed

### Critical Issues Resolved:
1. **Plain text password storage** → Secure bcrypt hashing
2. **Missing CSRF protection** → Complete CSRF middleware implementation
3. **XSS vulnerabilities** → Automatic HTML escaping and sanitization
4. **No rate limiting** → Token-bucket rate limiting per client IP
5. **Hardcoded secrets** → Environment variable configuration

## 🛡️ Security Best Practices

### For Developers

1. **Input Validation**
   ```go
   if err := security.ValidateUserInput(userInput); err != nil {
       return hyperchi.BadRequest("Invalid input.")
   }
   ```

2. **Secure Token Generation**
   ```go
   token, err := security.GenerateSecureToken(32)
   if err != nil {
       return fmt.Errorf("failed to generate token: %w", err)
   }
   ```

3. **Timing-Safe Comparisons**
   ```go
   valid := security.TimingSafeEqual(providedToken, expectedToken)
   ```

4. **Password Strength Assessment**
   ```go
   strength := security.CheckPasswordStrength(password)
   if strength < security.StrengthMedium {
       return errors.New("password too weak")
   }
   ```

5. **Content Sanitization**
   ```go
   // Escape HTML content
   safe := security.EscapeHTML(userContent)
   
   // Mark trusted content as safe
   trusted := security.MarkSafe("<b>Admin Message</b>")
   ```

6. **File Upload Validation**
   ```go
   result := security.NewUploadValidator(config).ValidateUpload(c.Request())
   if !result.Valid {
       return hyperchi.BadRequest(strings.Join(result.Errors, "; "))
   }
   ```

### For Deployment

1. **Use Environment Variables**
   ```bash
   export JWT_SECRET="$(openssl rand -base64 32)"
   export SESSION_SECRET="$(openssl rand -base64 32)"
   ```

2. **Configure Reverse Proxy**
   ```nginx
   # Add security headers
   add_header X-Frame-Options DENY;
   add_header X-Content-Type-Options nosniff;
   add_header X-XSS-Protection "1; mode=block";
   ```

3. **Database Security**
   ```go
   config := database.DefaultDBConfig()
   config.MaxOpenConns = 25  // Reasonable limit
   config.MaxIdleConns = 5   // Prevent connection exhaustion
   ```

## 🔍 Security Testing

### CSRF Testing
```bash
# Test CSRF protection
curl -X POST http://localhost:8080/api/data \
  -H "Content-Type: application/json" \
  -d '{"test": "data"}'
# Should return 403 Forbidden
```

### Rate Limiting Testing
```bash
# Test rate limiting
for i in {1..101}; do
  curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8080/
done
# Should show 429 after limit is reached
```

### XSS Testing
```bash
# Test XSS protection
curl -X POST http://localhost:8080/form \
  -d "input=<script>alert('xss')</script>"
# Script should be escaped in output
```

### File Upload Security Testing
```bash
# Test file size limits
curl -X POST http://localhost:8080/upload \
  -F "file=@large_file.bin" \
  -H "Authorization: Bearer $TOKEN"
# Should return 413 if file too large

# Test MIME type validation
curl -X POST http://localhost:8080/upload \
  -F "file=@malicious.exe" \
  -H "Authorization: Bearer $TOKEN"
# Should return 400 for disallowed type
```

### Input Validation Testing
```bash
# Test email validation
curl -X POST http://localhost:8080/register \
  -d "email=invalid-email&password=test123"
# Should return validation errors

# Test SQL injection prevention
curl -X POST http://localhost:8080/login \
  -d "email=admin'; DROP TABLE users; --&password=test"
# Should return invalid credentials, not SQL error
```

### Authentication Testing
```bash
# Test protected route without auth
curl -X GET http://localhost:8080/admin
# Should return 401 Unauthorized

# Test session expiry
curl -X GET http://localhost:8080/profile \
  -H "Authorization: Bearer expired_token"
# Should return 401 Unauthorized
```

## 📞 Reporting Security Issues

If you discover a security vulnerability in HyperChi:

1. **DO NOT** open a public issue
2. Email security concerns to the maintainers
3. Include steps to reproduce the issue
4. Allow time for the issue to be addressed before public disclosure

## 📚 Additional Resources

- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [Go Security Guide](https://github.com/guardrailsio/awesome-golang-security)
- [Web Security Basics](https://developer.mozilla.org/en-US/docs/Web/Security)

---

*Remember: Security is an ongoing process, not a one-time setup. Stay updated with security best practices and regularly review your application's security posture.*
