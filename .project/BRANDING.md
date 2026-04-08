# GERYON — BRANDING

> Visual identity and brand guidelines for Geryon — the three-bodied database proxy.

## 1. BRAND STORY

**Geryon** (Γηρυών) was a fearsome three-bodied giant in Greek mythology, guardian of a magnificent herd of red cattle on the island of Erytheia. Heracles faced Geryon as his 10th labor — one of the most formidable challenges.

In the Geryon proxy, the three bodies represent the three database protocols:
- **Body I** — PostgreSQL (the scholar)
- **Body II** — MySQL (the workhorse)
- **Body III** — MSSQL (the enterprise)

One entity. Three bodies. Every database connection flows through the giant.

## 2. TAGLINE

**Primary:** `Three Bodies. One Proxy. Every Connection.`

**Secondary options:**
- `The Three-Bodied Proxy`
- `One Giant. Three Protocols. Zero Limits.`
- `Guard Your Connections.`

## 3. COLOR PALETTE

### Primary Colors

| Name | Hex | Usage |
|---|---|---|
| **Geryon Red** | `#DC2626` | Primary brand color, CTA buttons, hero elements |
| **Titan Black** | `#0F0F0F` | Backgrounds, text, dark UI |
| **Bone White** | `#FAFAF9` | Light backgrounds, body text on dark |

### Body Colors (Protocol Identity)

| Body | Color Name | Hex | Usage |
|---|---|---|---|
| Body I (PostgreSQL) | **Elephant Blue** | `#336791` | PG-related UI, pool badges |
| Body II (MySQL) | **Dolphin Orange** | `#F29111` | MySQL-related UI, pool badges |
| Body III (MSSQL) | **Server Red** | `#CC2927` | MSSQL-related UI, pool badges |

### Supporting Colors

| Name | Hex | Usage |
|---|---|---|
| **Myth Gold** | `#D4A843` | Accents, premium features, cluster leader badge |
| **Stone Gray** | `#6B7280` | Secondary text, borders, disabled states |
| **Health Green** | `#16A34A` | Status: healthy, connected, success |
| **Warn Amber** | `#D97706` | Status: degraded, suspect, warning |
| **Error Crimson** | `#DC2626` | Status: down, error, critical |

### Dark Theme (Dashboard)

| Element | Color |
|---|---|
| Background | `#0F0F0F` |
| Surface | `#1A1A1A` |
| Card | `#242424` |
| Border | `#333333` |
| Text Primary | `#FAFAF9` |
| Text Secondary | `#9CA3AF` |

## 4. TYPOGRAPHY

| Usage | Font | Fallback |
|---|---|---|
| Logo / Headlines | **Inter** (700 Bold) | system-ui, sans-serif |
| Body | **Inter** (400 Regular) | system-ui, sans-serif |
| Code / Monospace | **JetBrains Mono** | ui-monospace, monospace |
| Dashboard | **Inter** | system-ui, sans-serif |

## 5. LOGO CONCEPT

### Primary Logo

**Concept:** A stylized three-bodied silhouette forming a unified shield shape, with three distinct heads/torsos merging into a single lower body. Each body subtly colored with its protocol's color (blue, orange, red) while the unified base is Geryon Red.

**Design principles:**
- Recognizable at 16×16 favicon size
- Works in single color (all Geryon Red)
- Works in full color (three body colors)
- Clean geometric shapes, no fine detail
- Shield/guardian connotation

### Logo Prompt (AI Image Generation)

```
Prompt 1 — Primary Logo:
A minimalist geometric logo of a three-bodied giant (Geryon from Greek mythology). Three stylized torso/head silhouettes merge into one unified lower form, creating a shield shape. Left body tinted #336791 (blue), center body tinted #F29111 (orange), right body tinted #CC2927 (red). Clean vector style, solid colors, no gradients, black background. Modern tech brand aesthetic, suitable for software logo. No text.

Prompt 2 — Icon/Favicon:
A minimal geometric icon showing three overlapping circles or rounded shapes forming a triangular unity, representing three database protocols. Colors: #336791, #F29111, #CC2927. Dark background #0F0F0F. Flat vector style, no shadows, no gradients. 512x512. Suitable for app icon and favicon.

Prompt 3 — Text Logo:
The word "GERYON" in bold geometric sans-serif font (Inter Bold style), with the 'G' containing three subtle vertical stripes in blue (#336791), orange (#F29111), and red (#CC2927). Rest of letters in #FAFAF9 on #0F0F0F background. Clean, modern, tech brand aesthetic.

Prompt 4 — Mascot (Fun Variant):
A friendly cartoon three-bodied giant character with a tech/cyberpunk aesthetic. Three heads with different expressions — one wearing glasses (scholar/PG), one with a hardhat (worker/MySQL), one with a tie (enterprise/MSSQL). Connected at the torso, standing like a guardian. Flat illustration style, Geryon Red (#DC2626) as dominant color with blue, orange, red accents per head.

Prompt 5 — Architecture Diagram Style:
A technical diagram-style logo showing three parallel vertical lines (representing three wire protocols) converging into a single point (the proxy), then fanning out to a single horizontal line (unified output). Colors: #336791, #F29111, #CC2927 for the three input lines, #DC2626 for the convergence point. Minimal, blueprint aesthetic on dark background.
```

### Nano Banana 2 Infographic Prompt

```
A modern flat-design product infographic for "Geryon" — a multi-database connection pooler proxy built in Go. Dark background (#0F0F0F). Header: "GERYON" in bold white text with three-color accent (blue, orange, red). Subheader: "Three Bodies. One Proxy. Every Connection." Feature highlights in card layout: "PostgreSQL + MySQL + MSSQL" (with protocol colors), "Session/Transaction/Statement Pooling", "Zero Dependencies — Single Binary", "Raft + Gossip Clustering", "REST + MCP + gRPC + Dashboard", "TLS/mTLS + Auth Interception". Bottom: "geryonproxy.com" and ECOSTACK TECHNOLOGY OÜ branding. Geryon Red (#DC2626) accent color. Clean, professional, developer-focused. 1080x1350 Instagram/social format.
```

## 6. ICONOGRAPHY

### Feature Icons (Dashboard & Docs)

| Feature | Icon Concept |
|---|---|
| PostgreSQL pool | Elephant silhouette (blue) |
| MySQL pool | Dolphin silhouette (orange) |
| MSSQL pool | Diamond/rhombus shape (red) |
| Connection | Two nodes with line |
| Pooling | Circles in a container |
| Clustering | Three connected nodes |
| Security/TLS | Lock/shield |
| Cache | Lightning bolt |
| Dashboard | Grid/chart |
| MCP | Plug/socket |
| Health | Heartbeat pulse |
| Config | Gear/wrench |

## 7. VOICE & TONE

### Brand Personality
- **Powerful** — "Three-bodied giant" energy, handles anything
- **Unified** — One solution for multiple problems
- **Reliable** — Guardian, protector of connections
- **Fun** — Mythological reference adds personality to infra tooling

### Writing Style
- Direct and confident
- Technical but accessible
- Mythological references welcome (not forced)
- Examples: "Release the Geryon", "Guard your connections", "Three bodies, zero worries"

### README Badge

```markdown
[![Geryon](https://img.shields.io/badge/Geryon-Three%20Bodies-DC2626?style=flat-square&logo=data:...)](https://geryonproxy.com)
```

## 8. SOCIAL MEDIA TEMPLATES

### Twitter/X Post Templates

```
🏛️ Introducing Geryon — the three-bodied database proxy.

One binary. Three protocols. Zero dependencies.

PostgreSQL + MySQL + MSSQL pooling, unified.

Built in pure Go 🦫

#golang #database #opensource
```

```
Why run 3 separate connection poolers when Geryon handles all of them?

🔵 PostgreSQL — Session/Transaction/Statement
🟠 MySQL — Full wire protocol
🔴 MSSQL — TDS 7.4+

Single binary. Raft clustering. MCP server.

geryonproxy.com
```

## 9. DOMAIN & DIGITAL PRESENCE

| Asset | Value |
|---|---|
| Domain | geryonproxy.com |
| GitHub | github.com/GeryonProxy |
| GitHub Repo | github.com/GeryonProxy/geryon |
| Docker Hub | geryonproxy/geryon |
| Twitter/X | @GeryonProxy |
| Logo files | /branding/ directory in repo |

## 10. ECOSTACK BRANDING

Geryon is an ECOSTACK TECHNOLOGY OÜ project. The ECOSTACK badge appears:
- In README footer
- On landing page footer
- In dashboard "About" section
- On infographic materials

```
Built with ❤️ by ECOSTACK TECHNOLOGY OÜ
ecostack.dev
```
