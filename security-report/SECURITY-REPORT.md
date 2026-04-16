# GeryonProxy Security Report

**Date:** 2026-04-16
**Scan Type:** Full Security Audit
**Phase:** Recon -> Hunt -> Verify -> Report
**Go Version:** 1.26.1 (upgrade to 1.26.2 recommended)

---

## Executive Summary

GeryonProxy has a minimal supply chain (5 direct dependencies, no CGO, no shell execution) and strong foundational security (SCRAM-SHA-256 with 120k PBKDF2 iterations, constant-time comparisons, TLS 1.2+).

This audit identified 2 critical, 4 high, 8 medium, 6 low findings requiring attention.

**Priority action items:**
1. Goroutine leak in SWIM protocol (CRIT-1)
2. Cross-mutex race in SWIM probe (CRIT-2)
3. Backend transaction orphaning (HIGH-1)
4. Missing defer cancel() leaks timers (HIGH-2)
5. Upgrade Go to 1.26.2 (4 CVEs)

**Overall posture:** Strong production-ready security. Previous findings FIXED.

**False Positives (Verified):**
- Global authMessage race - FALSE POSITIVE (dead code, never accessed)
- Session.lastQuery race - FALSE POSITIVE (already fixed)

---

## Critical Findings

### CRIT-1: Goroutine Leak in SWIM Protocol Indirect Probe

| Severity | CRITICAL | CWE | CWE-400 |
| File | internal/cluster/cluster.go:626-650 |

protocolRound() launches unbounded goroutines via go s.probe(target) without any tracking or limits.

**Impact:** No mechanism to limit concurrent probe goroutines. Under attack, unlimited goroutines exhaust memory/file descriptors.

**Remediation:** Add bounded semaphore:
var probeSem = make(chan struct{}, 100)

---

### CRIT-2: Cross-Mutex Race in SWIM Probe (VERIFIED CONFIRMED)

| Severity | CRITICAL | CWE | CWE-362 |
| File | internal/cluster/cluster.go:644-649 |

SwimGossip.probe() modifies target.LastSeen and target.State while holding only SwimGossip.mu, but these fields are also accessed by Cluster methods under Cluster.mu.

**Impact:** Cross-mutex race corrupts cluster state during concurrent operations.

**Remediation:** Use atomic.Value for Node fields, or acquire both locks in consistent order.

---

### CRIT-3: Global authMessage Race - FALSE POSITIVE

The global var authMessage string is never accessed - completely shadowed by local variables. Dead code only. NOT a race condition.

---

## High Findings

### HIGH-1: Backend Transaction Orphaning - No ROLLBACK on Timeout

| Severity | HIGH | CWE | CWE-400 |
| File | internal/pool/transaction.go:200-266 |

checkTimeouts() sets status to TxnAborted but does NOT send ROLLBACK to backend.

**Remediation:** Send ROLLBACK via AbortFunc on timeout.

---

### HIGH-2: Missing defer cancel() Leaks Timers

| Severity | HIGH | CWE | CWE-775 |
| File | internal/pool/pool.go:261-268 |

**Remediation:** Use defer cancel() immediately after context.WithTimeout.

---

### HIGH-3: TCP Connection Leak in Cluster RPC

| Severity | HIGH | CWE | CWE-404 |

**Remediation:** Implement connection pooling for cluster RPC.

---

### HIGH-4: Unencrypted Backend Connections (Passthrough Auth)

| Severity | HIGH | CWE | CWE-319 |

**Remediation:** Implement TLS for all backend connections.

---

### HIGH-5: Unauthenticated Raft Cluster Communication

| Severity | HIGH | CWE | CWE-306 |

**Remediation:** Implement mTLS or PSK for inter-node communication.

---

## Medium/Low Findings (Summary)

| ID | Finding | Severity | CWE |
|----|---------|----------|-----|
| MED-1 | Unauthenticated SWIM Gossip (UDP) | MEDIUM | CWE-306 |
| MED-2 | Weak PRNG for Raft Elections | MEDIUM | CWE-338 |
| MED-3 | Insecure Default 0.0.0.0 Binding | MEDIUM | CWE-16 |
| MED-4 | Slowloris Protection Gaps | MEDIUM | CWE-400 |
| MED-5 | No Connection Rate Limiting | MEDIUM | CWE-770 |
| MED-6 | Insecure TLS prefer Default | MEDIUM | CWE-309 |
| MED-7 | rand.Read Error Ignored in SCRAM | MEDIUM | CWE-390 |
| MED-8 | O(n^2) SQL Comment Stripping | MEDIUM | CWE-835 |
| LOW-1 | Password Buffer Zeroization Deferred | LOW | CWE-212 |
| LOW-2 | Query Log Path Traversal Edge Case | LOW | CWE-22 |
| LOW-3 | Unbounded Backend Slice Growth | LOW | CWE-400 |
| LOW-4 | Unbounded Auth Limiter Map | LOW | CWE-400 |
| LOW-5 | No Backend Connection Timeouts | LOW | CWE-400 |
| LOW-6 | Slowloris Write Deadline Missing | LOW | CWE-400 |

---

## Go Standard Library CVEs (ACTION REQUIRED)

Upgrade Go to 1.26.2

| CVE | Severity | Component |
|-----|----------|-----------|
| GO-2026-4866 | HIGH | crypto/x509 - Auth bypass |
| GO-2026-4947 | Medium | crypto/x509 |
| GO-2026-4946 | Medium | crypto/x509 |
| GO-2026-4870 | Medium | crypto/tls |

---

## Remediation Roadmap

| Priority | Finding | Est. Time |
|----------|---------|-----------|
| P0 | CRIT-1: Bound SWIM probe goroutines | 30 min |
| P0 | CRIT-2: Fix cross-mutex race | 30 min |
| P0 | HIGH-1: Send ROLLBACK on timeout | 30 min |
| P0 | HIGH-2: Add defer cancel() | 5 min |
| P0 | CVE: Upgrade Go to 1.26.2 | 5 min |
| P1 | HIGH-3: Cluster RPC connection pooling | 1 hr |
| P1 | HIGH-4: TLS for backend connections | 1 hr |
| P1 | HIGH-5: mTLS for Raft cluster | 1 hr |

---

## Previous Findings Status (2026-04-14)

All FIXED: CR-1, CR-2, H-1, H-2, H-4, H-5, M-4, L-2, L-3
REOPENED: H-3 (now HIGH-1)

---

Report generated: 2026-04-16
