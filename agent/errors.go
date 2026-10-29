package agent

import "errors"

var (
	ErrClosed          = errors.New("runtime ditutup")
	ErrSessionNotFound = errors.New("session tidak ditemukan")
	ErrSessionBusy     = errors.New("session sedang berjalan")
	ErrEmptyText       = errors.New("teks kosong")
	ErrUnknownAgent    = errors.New("agent tidak dikenal")
	ErrUnknownProvider = errors.New("provider tidak dikenal")
	ErrEmptyModel      = errors.New("model kosong")
)
