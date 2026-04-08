package main

import "embed"

//go:embed static/*
var DashboardFS embed.FS
