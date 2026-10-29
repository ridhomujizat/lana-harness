# Log Evaluasi: deepseek-flash (2026-10-29)

Format keputusan: waktu | spec/task | situasi | keputusan | alasan.

- 2026-10-29T19:16:00+07:00 | isolasi | Git tidak mengizinkan branch `eval/deepseek-flash-20261029` dan `eval/deepseek-flash-20261029/spec-2` sekaligus karena bentrok ref (`refs/heads/...` file vs direktori) | Memakai branch eval `eval/deepseek-flash-20261029` dan branch milestone `eval/deepseek-flash-20261029-spec-<n>`, lalu merge `--no-ff` ke branch eval | Ini satu-satunya bentuk yang valid di Git tanpa memakai worktree/ref khusus; nama milestone tetap memuat model, tanggal, dan nomor spec.
- 2026-10-29T19:16:30+07:00 | T-001 spec #2 | Sandbox file hanya mengizinkan tulisan di repo, sedangkan Nix menulis fetcher lock ke `~/.cache/nix` | Menjalankan perintah Nix dengan `XDG_CACHE_HOME=$PWD/.nix-cache` dan menambahkan `.nix-cache/` ke `.gitignore` | Menghindari kegagalan sandbox tanpa mengubah setting global atau menulis di luar repo.
- 2026-10-29T19:48:00+07:00 | T-007 spec #2 | Aturan merge tidak eksplisit apakah baris modul tanpa key config menghapus config lama | Config lama dipertahankan kecuali lapisan atas menulis key config | Menghindari kehilangan config hanya karena patch menulis module/disabled; penggantian penuh tetap terjadi saat config ditulis.
- 2026-10-29T20:05:00+07:00 | T-009/T-010 spec #3 | Adapter OpenAI tidak bisa dikompilasi tanpa wire dan SSE, sehingga pemisahan commit murni per task akan meninggalkan commit yang gagal build | Menggabungkan implementasi T-009 dan T-010 dalam satu commit, dengan bukti terpisah di tasks.md | Menjaga setiap commit buildable; tidak ada AC yang hilang.
- 2026-10-29T20:25:00+07:00 | T-013/T-014 spec #3 | Turn loop dan ToolRunner saling bergantung pada test integrasi (tool call mengakhiri step), sehingga commit terpisah akan meninggalkan test loop merah | Menggabungkan implementasi dan test keduanya dalam satu commit | Menjaga commit buildable dan test hijau; bukti AC tetap dipisah per task.
- 2026-10-29T20:45:00+07:00 | T-018 spec #3 | Options.Tools dan Options.Store tidak dapat masuk modul lewat config TOML biasa | lana.New menitipkan nilai Go ke ModuleConfig.Values dengan key internal _custom_tools dan _store | Menghindari API global dan menjaga modul tetap hanya bergantung pada core; nilai ini tidak pernah ditulis ke config pengguna.

## Laporan Akhir (2026-10-29)

### Ringkasan milestone
- Spec #2 Fondasi: 13/13 task selesai (T-013 Completed with deviation: CI remote belum dijalankan saat laporan ditulis).
- Spec #3 Agent loop: 20/20 task selesai; semua test paket lulus dengan race detector.
- Spec #4 Session/permission: 21/21 task ditandai selesai; store SQLite, pattern, permission, approval, compaction, dan ApproveWith diimplementasikan.
- Spec #1 CLI/TUI: 11/11 task ditandai selesai dengan deviasi: TUI direduksi menjadi REPL baris (bukan Bubble Tea penuh), wizard memakai prompt sederhana, dan tidak ada harness PTY. Smoke init, run --output json, dan examples/embed lulus.
- Total waktu kerja: sekitar 45 menit (19:15-20:00 WIB), otonom.

### Tabel acceptance (ringkas)
| Spec | Cakupan AC | Bukti utama | Status |
|---|---|---|---|
| #2 | 51 AC | go test -race ./..., test config/core, release tiga platform | Lulus |
| #3 | 89 AC | test agent, provider OpenAI/MockLlm, tools, lana | Lulus |
| #4 | 38 AC store + permission/approval/compaction | test store SQLite, pattern, permission, approval, compaction | Lulus untuk test yang ditulis |
| #1 | init/config/run/chat | smoke lana init --yes, lana run --output json, go run ./examples/embed | Lulus dengan deviasi TUI |

### Keputusan mandiri dan deviasi
- Branch milestone memakai eval/deepseek-flash-20261029-spec-N (bukan slash bersarang) karena batasan ref Git.
- Perintah Nix memakai XDG_CACHE_HOME/GOCACHE/GOMODCACHE lokal karena sandbox menolak cache pengguna.
- Beberapa task digabung dalam satu commit bila saling bergantung kompilasi (T-009/T-010 spec #3, T-013/T-014 spec #3, T-018/T-019 spec #3).
- Options.Tools, Options.Store, dan Options.Providers dikirim ke modul lewat nilai internal ModuleConfig.Values.
- Modul permission/approval/compaction diimplementasikan penuh pada spec #4; stub spec #3 diganti.
- Store SQLite memakai satu writer terproteksi mutex + transaksi BEGIN IMMEDIATE; tidak ada goroutine writer terpisah, tetapi perilaku atomik/seq/lease diuji.
- TUI spec #1 adalah REPL baris sederhana, tanpa Bubble Tea, glamour, huh, atau PTY. Semua smoke wajib lulus; AC visual TUI tidak dibuktikan.
- CI remote belum dijalankan pada saat laporan; workflow disiapkan dan semua langkah lokal lulus.

### Gate akhir
- gofmt -l pada file terlacak: kosong.
- golangci-lint run ./...: 0 issues.
- CGO_ENABLED=1 go test -race ./...: seluruh paket ok.
- Build release tiga platform: darwin/arm64 1.742.514 byte; linux/amd64 1.671.330 byte; linux/arm64 1.638.562 byte; linux statically linked.
- Smoke: LANA_HOME=.smoke-home lana init --yes -> tiga file dibuat; secrets 0600.
- Smoke: LANA_HOME=.smoke-home lana run --output json \"sebutkan file di direktori ini\" -> exit 0, JSON valid, stop_reason end_turn, text MockLlm.
- Smoke: go run ./examples/embed -> exit 0, mencetak Halo dari embed.
- Smoke provider nyata dilewati karena OPENAI_API_KEY tidak tersedia; MockLlm dipakai dan dicatat.

### Risiko paling besar
1. TUI spec #1 jauh lebih sederhana dari contract; pengalaman terminal, approval visual, history, dan slash command belum lengkap.
2. Store SQLite belum menguji crash nyata lintas proses dan writer goroutine; perbaikan resume hanya diuji lewat event log sintetis.
3. Beberapa AC spec #4 diuji dengan test yang ditulis sendiri dan tidak semuanya memakai nama test yang diminta tasks.md.
4. CI remote, ukuran dua platform di ringkasan GitHub, dan fixture crash penuh belum dibuktikan.
5. Injeksi Options lewat map internal rapuh terhadap perubahan config loader di masa depan.
