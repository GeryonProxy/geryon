# Dependency Audit Report

**Project:** GeryonProxy  
**Audit Date:** 2026-04-13  
**Go Version:** 1.26.1  
**Policy:** Zero-dependency philosophy (stdlib-only core)

---

## Summary

| Metric | Value |
|--------|-------|
| Total dependencies | 3 (direct: 2, transitive: 1) |
| Direct dependencies | 2 |
| Transitive dependencies | 1 |
| Ecosystems scanned | Go modules |
| Known vulnerabilities found | 0 |
| Build-time code generation | None (no //go:generate directives) |
| CGO usage | Disabled (`CGO_ENABLED=0`) |

---

## DEP-001: Module Integrity Validation

**Status:** PASSED

All dependencies have corresponding `go.mod` and `go.sum` entries with verified hashes.

**go.mod entries:**
- `golang.org/x/term v0.36.0` — direct
- `golang.org/x/time v0.15.0` — direct
- `golang.org/x/sys v0.37.0` — indirect (pulled in by term/time)

**go.sum validation:**
```
golang.org/x/sys  v0.37.0  h1:fdNQudmxPjkdUTPnLn5mdQv7Zwvbvpaxqs831goi9kQ=
golang.org/x/term v0.36.0  h1:zMPR+aF8gfksFprF/Nc/rd1wRS1EI6nDBGyWAvDzx2Q=
golang.org/x/time v0.15.0  h1:bbrp8t3bGUeFOx08pvsMYRTCVSMk89u4tKbNOZbp88U=
```

All entries include both the `h1:` hash (hex-encoded SHA-256 of the module zip) and the `/go.mod` hash for integrity verification.

---

## DEP-002: golang.org/x/sys Indirect Dependency — System-Level Access

**Status:** INFO (No Action Required)

`golang.org/x/sys v0.37.0` is an indirect dependency required by both `golang.org/x/term` and `golang.org/x/time`. It provides low-level system calls (file descriptor operations, terminal size queries, time syscalls).

**Risk Assessment:** LOW

- It is not imported directly by any project source files.
- All builds consistently use `CGO_ENABLED=0`, preventing any C runtime linkage.
- No unsafe pointer operations or direct syscall usage observed in the codebase.
- The `golang.org/x/*` packages are maintained by the Go team and receive prompt security patches.

---

## DEP-003: CGO Enforcement — Static Builds

**Status:** PASSED

All build artifacts enforce `CGO_ENABLED=0`:

- `Dockerfile`: `RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o geryon ./cmd/geryon`
- `Makefile`: `CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/geryon ./cmd/geryon`
- `.github/workflows/release.yml`: `CGO_ENABLED: 0`
- `CLAUDE.md`: Documents static-only build requirement

**Benefit:** Eliminates C runtime attack surface. No libc, no CGO FFI vulnerabilities.

---

## DEP-004: Build-Time Code Generation

**Status:** PASSED

No `//go:generate` directives found anywhere in the codebase.

**Implication:** No reliance on `go generate` for any code generation (protobuf, stringers, etc.). Build is fully deterministic without running code generation tooling.

---

## DEP-005: Command Injection — os/exec Usage

**Status:** NOT APPLICABLE

No usage of `os/exec` or `exec.Command` found in the codebase.

This eliminates an entire class of command injection vulnerabilities.

---

## DEP-006: SSRF Risk — net/http Outbound HTTP Clients

**Status:** NOT APPLICABLE (Tests Only)

All `net/http` usage (in test files only) is for local test server communication:

```go
// Test files only — localhost connections to built-in test servers
resp, err := http.Post("http://"+cfg.Listen+"/grpc.health.v1.Health/Check", ...)
resp, err := http.Get("http://" + cfg.Listen + "/api/v1/health")
```

**Risk Assessment:** NONE

- No outbound HTTP client creation in production code.
- No HTTP proxy configuration found.
- No URL parsing of untrusted remote resources.
- All HTTP requests target `localhost` or loopback addresses in test contexts only.

---

## DEP-007: Cryptographic Operations — stdlib Only

**Status:** PASSED

All crypto usage is via Go standard library only:

| Package | Usage Location |
|---------|---------------|
| `crypto/tls` | `internal/proxy/listener.go`, `internal/tlsutil/tls.go`, `internal/pool/pool.go` |
| `crypto/x509` | `internal/auth/cert.go`, `internal/tlsutil/config.go`, `internal/proxy/listener.go` |
| `crypto/ecdsa` | `internal/tlsutil/config.go` |
| `crypto/rsa` | (via x509) |

**Risk Assessment:** LOW

- No third-party crypto libraries — stdlib `crypto/*` is maintained by the Go team.
- TLS configuration uses Go's built-in cipher suite defaults with TLS 1.2+.
- Certificate validation flows through standard `crypto/x509` pool mechanisms.

---

## Findings Summary

| Finding | Severity | Action |
|---------|----------|--------|
| DEP-001 Module integrity | PASSED | None — all hashes verified in go.sum |
| DEP-002 golang.org/x/sys indirect dep | INFO | None — low risk, maintained by Go team |
| DEP-003 CGO disabled | PASSED | None — static builds enforced |
| DEP-004 No code generation | PASSED | None — no //go:generate directives |
| DEP-005 Command injection | NOT APPLICABLE | No exec usage found |
| DEP-006 SSRF | NOT APPLICABLE | All HTTP usage is localhost tests |
| DEP-007 Crypto stdlib only | PASSED | None — no third-party crypto deps |

---

## Conclusion

**GeryonProxy maintains a minimal dependency surface aligned with its zero-dependency philosophy.** The two direct dependencies (`golang.org/x/term`, `golang.org/x/time`) and their transitive dependency (`golang.org/x/sys`) are low-risk, well-maintained Go standard library packages. The project enforces static builds via `CGO_ENABLED=0`, eliminating C runtime exposure.

No known vulnerabilities exist in the dependency tree. The primary security posture for this project is determined by the code itself (the proxy, auth, and TLS implementation), not by its dependencies.
