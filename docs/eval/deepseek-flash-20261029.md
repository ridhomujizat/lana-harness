# Log Evaluasi: deepseek-flash (2026-10-29)

Format keputusan: waktu | spec/task | situasi | keputusan | alasan.

- 2026-10-29T19:16:00+07:00 | isolasi | Git tidak mengizinkan branch `eval/deepseek-flash-20261029` dan `eval/deepseek-flash-20261029/spec-2` sekaligus karena bentrok ref (`refs/heads/...` file vs direktori) | Memakai branch eval `eval/deepseek-flash-20261029` dan branch milestone `eval/deepseek-flash-20261029-spec-<n>`, lalu merge `--no-ff` ke branch eval | Ini satu-satunya bentuk yang valid di Git tanpa memakai worktree/ref khusus; nama milestone tetap memuat model, tanggal, dan nomor spec.
- 2026-10-29T19:16:30+07:00 | T-001 spec #2 | Sandbox file hanya mengizinkan tulisan di repo, sedangkan Nix menulis fetcher lock ke `~/.cache/nix` | Menjalankan perintah Nix dengan `XDG_CACHE_HOME=$PWD/.nix-cache` dan menambahkan `.nix-cache/` ke `.gitignore` | Menghindari kegagalan sandbox tanpa mengubah setting global atau menulis di luar repo.
