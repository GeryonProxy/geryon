# Dependency Audit Report

**Project:** GeryonProxy
**Audit Date:** 2026-04-18
**Go Version:** 1.26.1
**Policy:** Zero-dependency philosophy (stdlib-only core)

---

## Summary

| Metric | Value |
|--------|-------|
| Total direct dependencies | 2 |
| Total transitive dependencies | 1 |
| Known vulnerabilities | 0 |
| CGO usage | Disabled (`CGO_ENABLED=0`) |

---

## Dependencies

| Module | Version | Type | Risk |
|--------|---------|------|------|
| `golang.org/x/term` | v0.36.0 | Direct | Low |
| `golang.org/x/time` | v0.15.0 | Direct | Low |
| `golang.org/x/sys` | v0.37.0 | Indirect | Low |

## Key Findings

1. **Custom YAML parser** -- no yaml library imported, immune to yaml.v2/v3 CVEs
2. **No `os/exec` usage** -- zero command injection surface
3. **CGO disabled** -- static builds, no C runtime attack surface
4. **No third-party crypto** -- stdlib `crypto/*` only
5. **embed.FS for static assets** -- no embedded secrets
6. **No `//go:generate` directives** -- deterministic builds

## Go Standard Library CVEs

Go 1.26.1 has known CVEs. Upgrade to 1.26.2 recommended:

| CVE | Severity | Component |
|-----|----------|-----------|
| GO-2026-4866 | HIGH | crypto/x509 - Auth bypass |
| GO-2026-4947 | Medium | crypto/x509 |
| GO-2026-4946 | Medium | crypto/x509 |
| GO-2026-4870 | Medium | crypto/tls |

---

*Generated: 2026-04-18*
