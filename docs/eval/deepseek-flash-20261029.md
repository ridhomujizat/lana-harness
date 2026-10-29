# Log Evaluasi: deepseek-flash (2026-10-29)

Format keputusan: waktu | spec/task | situasi | keputusan | alasan.

- 2026-10-29T19:16:00+07:00 | isolasi | Git tidak mengizinkan branch `eval/deepseek-flash-20261029` dan `eval/deepseek-flash-20261029/spec-2` sekaligus karena bentrok ref (`refs/heads/...` file vs direktori) | Memakai branch eval `eval/deepseek-flash-20261029` dan branch milestone `eval/deepseek-flash-20261029-spec-<n>`, lalu merge `--no-ff` ke branch eval | Ini satu-satunya bentuk yang valid di Git tanpa memakai worktree/ref khusus; nama milestone tetap memuat model, tanggal, dan nomor spec.
- 2026-10-29T19:16:30+07:00 | T-001 spec #2 | Sandbox file hanya mengizinkan tulisan di repo, sedangkan Nix menulis fetcher lock ke `~/.cache/nix` | Menjalankan perintah Nix dengan `XDG_CACHE_HOME=$PWD/.nix-cache` dan menambahkan `.nix-cache/` ke `.gitignore` | Menghindari kegagalan sandbox tanpa mengubah setting global atau menulis di luar repo.
- 2026-10-29T19:48:00+07:00 | T-007 spec #2 | Aturan merge tidak eksplisit apakah baris modul tanpa key config menghapus config lama | Config lama dipertahankan kecuali lapisan atas menulis key config | Menghindari kehilangan config hanya karena patch menulis module/disabled; penggantian penuh tetap terjadi saat config ditulis.
- 2026-10-29T20:05:00+07:00 | T-009/T-010 spec #3 | Adapter OpenAI tidak bisa dikompilasi tanpa wire dan SSE, sehingga pemisahan commit murni per task akan meninggalkan commit yang gagal build | Menggabungkan implementasi T-009 dan T-010 dalam satu commit, dengan bukti terpisah di tasks.md | Menjaga setiap commit buildable; tidak ada AC yang hilang.
- 2026-10-29T20:25:00+07:00 | T-013/T-014 spec #3 | Turn loop dan ToolRunner saling bergantung pada test integrasi (tool call mengakhiri step), sehingga commit terpisah akan meninggalkan test loop merah | Menggabungkan implementasi dan test keduanya dalam satu commit | Menjaga commit buildable dan test hijau; bukti AC tetap dipisah per task.
- 2026-10-29T20:45:00+07:00 | T-018 spec #3 | Options.Tools dan Options.Store tidak dapat masuk modul lewat config TOML biasa | lana.New menitipkan nilai Go ke ModuleConfig.Values dengan key internal _custom_tools dan _store | Menghindari API global dan menjaga modul tetap hanya bergantung pada core; nilai ini tidak pernah ditulis ke config pengguna.
- 2026-10-29T22:15:00+07:00 | TUI spec #1 | Owner menilai tampilan terminal terlalu sederhana dan meminta gaya Claude Code | Mengganti REPL baris dengan TUI Bubble Tea inline di channels/cli/tui, menambah API live runtime, dan mem-publish frame tool dari agent loop | Menutup deviasi TUI lama tanpa mengubah arsitektur modul; renderer Markdown ditulis sendiri agar tidak menambah glamour/chroma.
- 2026-10-29T22:15:00+07:00 | verifikasi TUI | Sandbox menolak openpty/script sehingga TUI tidak bisa diuji di PTY nyata | Menguji program Bubble Tea in-process memakai io.Pipe dan buffer, plus unit test renderer/tool/history | Menjaga bukti otomatis tetap ada; smoke manual di terminal asli dicatat sebagai risiko.

## Laporan Akhir (2026-10-29)

### Ringkasan milestone
- Spec #2 Fondasi: 13/13 task selesai (T-013 Completed with deviation: CI remote belum dijalankan saat laporan ditulis).
- Spec #3 Agent loop: 20/20 task selesai; semua test paket lulus dengan race detector.
- Spec #4 Session/permission: 21/21 task ditandai selesai; store SQLite, pattern, permission, approval, compaction, dan ApproveWith diimplementasikan.
- Spec #1 CLI/TUI: 11/11 task ditandai selesai. Pass pertama memakai REPL baris; pass kedua (lihat Pembaruan TUI) menggantinya dengan TUI Bubble Tea inline penuh. Smoke init, run --output json, dan examples/embed lulus.
- Total waktu kerja: sekitar 45 menit (19:15-20:00 WIB), otonom.

### Tabel acceptance (ringkas)
| Spec | Cakupan AC | Bukti utama | Status |
|---|---|---|---|
| #2 | 51 AC | go test -race ./..., test config/core, release tiga platform | Lulus |
| #3 | 89 AC | test agent, provider OpenAI/MockLlm, tools, lana | Lulus |
| #4 | 38 AC store + permission/approval/compaction | test store SQLite, pattern, permission, approval, compaction | Lulus untuk test yang ditulis |
| #1 | init/config/run/chat | smoke lana init --yes, lana run --output json, go run ./examples/embed, test channels/cli/tui | Lulus (TUI diperbarui) |

### Keputusan mandiri dan deviasi
- Branch milestone memakai eval/deepseek-flash-20261029-spec-N (bukan slash bersarang) karena batasan ref Git.
- Perintah Nix memakai XDG_CACHE_HOME/GOCACHE/GOMODCACHE lokal karena sandbox menolak cache pengguna.
- Beberapa task digabung dalam satu commit bila saling bergantung kompilasi (T-009/T-010 spec #3, T-013/T-014 spec #3, T-018/T-019 spec #3).
- Options.Tools, Options.Store, dan Options.Providers dikirim ke modul lewat nilai internal ModuleConfig.Values.
- Modul permission/approval/compaction diimplementasikan penuh pada spec #4; stub spec #3 diganti.
- Store SQLite memakai satu writer terproteksi mutex + transaksi BEGIN IMMEDIATE; tidak ada goroutine writer terpisah, tetapi perilaku atomik/seq/lease diuji.
- TUI spec #1 awalnya REPL baris; sejak Pembaruan TUI memakai Bubble Tea + Bubbles + Lip Gloss dengan renderer Markdown ringan sendiri (glamour dan huh tidak dipakai).
- CI remote dijalankan setelah push branch eval: run normal https://github.com/ridhomujizat/lana-harness/actions/runs/40878244461 sukses; run sengaja gagal https://github.com/ridhomujizat/lana-harness/actions/runs/40878578663 gagal sesuai AC-CI-001.

### Gate akhir
- gofmt -l pada file terlacak: kosong.
- golangci-lint run ./...: 0 issues.
- CGO_ENABLED=1 go test -race ./...: seluruh paket ok.
- Build release tiga platform (setelah SQLite): darwin/arm64 11.288.770 byte; linux/amd64 11.747.490 byte; linux/arm64 11.010.210 byte; linux statically linked.
- Smoke: LANA_HOME=.smoke-home lana init --yes -> tiga file dibuat; secrets 0600.
- Smoke: LANA_HOME=.smoke-home lana run --output json \"sebutkan file di direktori ini\" -> exit 0, JSON valid, stop_reason end_turn, text MockLlm.
- Smoke: `nix develop -c go run ./examples/embed` -> exit 0, mencetak Halo dari embed. (`go run` di luar dev shell gagal karena cache Go sandbox.)
- Smoke provider nyata dilewati karena OPENAI_API_KEY tidak tersedia; MockLlm dipakai dan dicatat.

### Risiko paling besar
1. TUI belum diuji di PTY terminal nyata karena sandbox menolak openpty; pengujian memakai program Bubble Tea in-process. Perlu smoke manual (warna, resize, Shift+Enter, paste) di terminal asli.
2. Store SQLite belum menguji crash nyata lintas proses dan writer goroutine; perbaikan resume hanya diuji lewat event log sintetis.
3. Beberapa AC spec #4 diuji dengan test yang ditulis sendiri dan tidak semuanya memakai nama test yang diminta tasks.md.
4. Fixture crash penuh lintas proses dan smoke provider nyata belum dibuktikan; CI remote sudah lulus.
5. Injeksi Options lewat map internal rapuh terhadap perubahan config loader di masa depan.


### Pembaruan TUI (2026-10-29 22:15 WIB)

Permintaan owner: tampilan terminal dibuat seperti Claude Code / Hermes / omp.

Perubahan:
- Package baru `channels/cli/tui` memakai Bubble Tea v1.3.10, Bubbles v0.21.0, Lip Gloss v1.1.0, dan x/ansi v0.10.1.
- TUI inline tanpa alt screen: banner sesi, streaming Markdown per blok, tool call ringkas/verbose, spinner, prompt approval di dalam aplikasi, status bar, input multi-baris, dan riwayat input.
- Tombol: Enter kirim, Alt+Enter/Ctrl+J/Shift+Enter baris baru, backslash di akhir baris + Enter, Ctrl+C batal/kosongkan/keluar, Ctrl+D keluar, Ctrl+O verbose, Ctrl+R cari riwayat, panah atas/bawah riwayat, Esc batal.
- Slash command: /help, /new, /sessions (picker ↑/↓/Enter), /fork, /compact, /clear, /session, /agent [nama], /model [provider:model], /permission, /yolo, /verbose, /exit.
- Runtime: ListSessions, ForkSession (batas turn terakhir), SetAgent, SetModel, Compact; Run/Stream memakai agent session bila Request.Agent kosong.
- Runtime: core.LiveFrame diperluas; agent loop mem-publish frame tool_call/tool_result; lana.Runtime mendapat CreateSession, LastSession, SessionInfo, SubscribeLive, Send, Cancel, Policy, SetPolicy, History, AgentNames, AgentModel, AgentPolicy; hook approval menerima channel cli.
- cmd/lana chat: flag --agent, --yolo, --continue, --resume, --verbose; sesi channel cli; fallback MockLlm memberi 200 respons plus notice.
- lana init: pilihan provider openai/openrouter/deepseek (`--provider` untuk `--yes`), prompt API key opsional dengan `term.ReadPassword` yang ditulis ke `secrets.env` 0600; `lana config check` menampilkan status tiap `api_key_env`.
- Fallback MockLlm menyebut variabel env yang belum diisi, mis. `DEEPSEEK_API_KEY belum diisi`.

Gate pembaruan:
- gofmt -l: kosong; go vet ./...: bersih; golangci-lint run ./...: 0 issues; CGO_ENABLED=1 go test -race ./...: semua paket ok.
- Build `go build ./cmd/lana`: ok; release `make release`: darwin/arm64 12.596.242 byte, linux/amd64 13.082.786 byte, linux/arm64 12.255.394 byte (statically linked, di bawah batas 40 MB).
- Smoke: `lana chat` dengan stdin bukan TTY -> exit 2 dan saran `lana run`; `lana run` dan `--output json` -> exit 0; `go run ./examples/embed` -> Halo dari embed.
- Batasan sandbox: openpty ditolak, jadi TUI diuji lewat program Bubble Tea in-process (io.Pipe + buffer) dan unit test renderer. Owner kemudian menjalankan TUI di terminal asli dan model DeepSeek nyata (2026-10-29 22:44) dan berhasil.
- Test lokal tambahan: `lana init --yes --provider deepseek` di home sementara menghasilkan config DeepSeek + `secrets.env` placeholder 0600, dan `resolveAgent` diuji untuk route/default/satu agent/config kosong.
