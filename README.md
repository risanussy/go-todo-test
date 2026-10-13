# Todo List Go

Aplikasi monolith CRUD todo list sederhana menggunakan Go, MySQL, dan Bootstrap.

## Persyaratan

- Go 1.23 atau lebih baru
- MySQL 8

## Menjalankan aplikasi

1. Buat database dan tabel:

   ```powershell
   mysql -u root -p < schema.sql
   ```

2. Salin konfigurasi environment:

   ```powershell
   Copy-Item .env.example .env
   ```

3. Sesuaikan kredensial MySQL di `.env`.

4. Unduh dependency dan jalankan:

   ```powershell
   go mod tidy
   go run .
   ```

5. Buka `http://localhost:8080`.

Tabel `todos` juga dibuat otomatis saat aplikasi dimulai. Database-nya sendiri harus sudah tersedia.
