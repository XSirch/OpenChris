package web

import "embed"

//go:embed *.html *.css *.js fonts/*
var Files embed.FS
