#!/bin/bash
cd "$(dirname "$0")"
git add -A
git commit -m "feat: add correlation ID tracing infrastructure

Add foundational support for distributed tracing and request correlation:

- internal/context: Context helpers for correlation ID propagation
- internal/tracing: Tracing configuration and span types (OpenTelemetry-ready)
- internal/proxy: Add correlationID to Relay struct, auto-generated per connection

fix: add retry logic with exponential backoff to SWIM probe

The probe function now retries up to 3 times with exponential backoff
(50ms, 100ms, 200ms) before falling back to indirect probing. This fixes
the timing-dependent test TestCluster_probe_SuccessfulConnection.

docs: add ARCHITECTURE.md and IMPLEMENTATION_ROADMAP.md

Comprehensive documentation covering:
- Three Bodies architecture (PostgreSQL, MySQL, MSSQL)
- Component hierarchy and package responsibilities
- Connection pooling modes (Session/Transaction/Statement)
- Routing, clustering (Raft+SWIM), security, configuration
- Mermaid diagrams for data flow visualization
- Detailed production roadmap with 5 phases, 68 tasks, 19 appendices
- Runbook, troubleshooting guide, and FAQ"