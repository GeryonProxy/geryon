# sc-jwt Security Check Results

## Finding: No JWT Usage

GeryonProxy does **not** use JWTs (JSON Web Tokens) anywhere in the codebase.

## Authentication Mechanism

The project uses a simple **Bearer token** authentication mechanism for admin APIs:

- **Dashboard** (`internal/api/dashboard/server.go:243`):
  ```go
  if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.authToken)) != 1 {
      http.Error(w, "Unauthorized", http.StatusUnauthorized)
      return
  }
  ```

- Tokens are static strings configured in `config.Auth.Token` via YAML configuration
- No JWT library imports found in the codebase
- No JWT parsing/validation code exists

## Algorithm Confusion

**Not applicable** - No JWT handling means no algorithm confusion vulnerability.

JWT algorithm confusion attacks typically exploit asymmetric/symmetric algorithm mismatches (e.g., `alg: none` or switching RS256 to HS256). Since GeryonProxy uses simple string comparison for Bearer tokens, this class of vulnerability does not apply.

## Token Validation

Token comparison uses `crypto/subtle.ConstantTimeCompare`, which provides protection against timing attacks. This is the correct approach for comparing secrets.

## Recommendations

1. **Current implementation is secure** for its design goal (static token auth)
2. If JWT support is added in the future:
   - Always specify expected algorithm (`jwt.WithValidMethods([]string{"RS256"})`)
   - Validate algorithm header matches expectation
   - Never trust the `alg` header from untrusted input
   - Use `jwt.WithIssuer()` and `jwt.WithAudience()` validation

## Conclusion

**No issues found.** GeryonProxy does not use JWTs and is not vulnerable to JWT-related attacks.