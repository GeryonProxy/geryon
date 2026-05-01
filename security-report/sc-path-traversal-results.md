# sc-path-traversal Security Check Results

## Summary

Checked GeryonProxy for path traversal vulnerabilities across:
- Config file path handling (cmd/geryon/main.go)
- Config hot-reload file watching (internal/config/watcher.go)
- TLS certificate loading (internal/tlsutil/tls.go)
- Query log file paths (internal/logger/querylog.go)
- Dashboard static file serving (internal/api/dashboard/server.go)

## Findings

### Config Path (main.go)
The config path is sourced from the `--config` command-line flag, not from client input:

```go
safeConfigPath := filepath.Clean(*configPath)
cfg, err := config.Load(safeConfigPath)
```

`filepath.Clean` normalizes the path (removes `..` components after symlink resolution). The path is set at startup by the operator, not by clients. **No vulnerability.**

### Config Hot-Reload (watcher.go)
The watcher stores the already-cleaned path at initialization:

```go
return &Watcher{
    path:     filepath.Clean(path),
    ...
}
```

Subsequent file reads use this stored clean path. **No vulnerability.**

### TLS Certificate Loading (tlsutil/tls.go)
TLS file paths (cert_file, key_file, ca_file) are loaded directly from the config:

```go
cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
caCert, err := os.ReadFile(cfg.CAFile)
```

**Note**: There is no directory boundary validation. If an operator uses a config with `cert_file: ../../etc/private_key`, the file would be read. However, since the config file itself is operator-controlled, this is not a client-exploitable path traversal. This is operational security (ensure your config file is secure), not a code vulnerability.

### Query Log Paths (logger/querylog.go)
Log paths are constructed using `filepath.Join`:

```go
slowLogPath := filepath.Join(config.Directory, "slow.log")
allLogPath := filepath.Join(config.Directory, "all.log")
jsonLogPath := filepath.Join(config.Directory, "queries.json")
```

**Note**: The `config.Directory` from the config file is not validated to be within an allowed directory. A malicious config could set `directory: ../../etc` and write log files there. Again, this requires operator-controlled config, not client input. **Not exploitable by clients.**

### Dashboard Static Files (api/dashboard/server.go)
Static files are served from an embedded filesystem:

```go
//go:embed static/*
var staticFS embed.FS
...
mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))
```

The dashboard uses `embed.FS` - files are compiled into the binary at build time. **No path traversal possible** as the files don't exist on disk and cannot be modified at runtime.

### Dashboard Config Write (handleConfigFile)
The dashboard can write the config file via `handleConfigFile`:

```go
tmpPath := configPath + ".tmp"
if err := os.WriteFile(tmpPath, data, 0600); err != nil { ... }
if err := os.Rename(tmpPath, configPath); err != nil { ... }
```

The `configPath` is set once at startup from the main config path. **No path traversal** - the path is not derived from client input.

---

## sc-file-upload Check

GeryonProxy is a database connection pooler, not a file server. File upload is not applicable.

**Dashboard static files**: Served via `embed.FS` (compiled into binary). Cannot be replaced at runtime. **Not applicable.**

---

## Conclusion

**No path traversal vulnerabilities found.**

All file paths in GeryonProxy are controlled by:
1. Command-line flags set at startup (operator-controlled)
2. Configuration file set by the operator

No file paths are derived from client input. The embedded filesystem for static assets cannot be modified at runtime. The architecture prevents client-controlled path manipulation.
