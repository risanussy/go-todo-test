package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
)

type Todo struct {
	ID        int64
	Title     string
	Completed bool
	CreatedAt time.Time
}

type App struct {
	db        *sql.DB
	templates *template.Template
}

func main() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("load .env: %v", err)
	}

	db, err := openDatabase()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := createTable(db); err != nil {
		log.Fatalf("create todos table: %v", err)
	}

	templates, err := template.ParseGlob("templates/*.html")
	if err != nil {
		log.Fatalf("parse templates: %v", err)
	}

	app := &App{db: db, templates: templates}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", app.index)
	mux.HandleFunc("POST /todos", app.createTodo)
	mux.HandleFunc("POST /todos/{id}/update", app.updateTodo)
	mux.HandleFunc("POST /todos/{id}/toggle", app.toggleTodo)
	mux.HandleFunc("POST /todos/{id}/delete", app.deleteTodo)

	port := envOrDefault("APP_PORT", "8080")
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           loggingMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("server berjalan di http://localhost:%s", port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
}

func openDatabase() (*sql.DB, error) {
	config := mysql.NewConfig()
	config.User = os.Getenv("DB_USER")
	config.Passwd = os.Getenv("DB_PASSWORD")
	config.Net = "tcp"
	config.Addr = fmt.Sprintf("%s:%s", envOrDefault("DB_HOST", "127.0.0.1"), envOrDefault("DB_PORT", "3306"))
	config.DBName = os.Getenv("DB_NAME")
	config.ParseTime = true
	config.Loc = time.Local
	config.Collation = "utf8mb4_unicode_ci"
	config.ClientFoundRows = true

	if config.User == "" || config.DBName == "" {
		return nil, errors.New("DB_USER dan DB_NAME wajib diisi di .env")
	}

	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(3 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	return db, nil
}

func createTable(db *sql.DB) error {
	const query = `
		CREATE TABLE IF NOT EXISTS todos (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			title VARCHAR(255) NOT NULL,
			completed BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
	_, err := db.Exec(query)
	return err
}

func (app *App) index(writer http.ResponseWriter, request *http.Request) {
	rows, err := app.db.QueryContext(request.Context(), `SELECT id, title, completed, created_at FROM todos ORDER BY created_at DESC`)
	if err != nil {
		http.Error(writer, "Gagal mengambil data todo", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var todos []Todo
	for rows.Next() {
		var todo Todo
		if err := rows.Scan(&todo.ID, &todo.Title, &todo.Completed, &todo.CreatedAt); err != nil {
			http.Error(writer, "Gagal membaca data todo", http.StatusInternalServerError)
			return
		}
		todos = append(todos, todo)
	}
	if err := rows.Err(); err != nil {
		http.Error(writer, "Gagal membaca data todo", http.StatusInternalServerError)
		return
	}

	data := struct {
		Todos     []Todo
		Total     int
		Completed int
	}{Todos: todos, Total: len(todos)}
	for _, todo := range todos {
		if todo.Completed {
			data.Completed++
		}
	}

	if err := app.templates.ExecuteTemplate(writer, "index.html", data); err != nil {
		log.Printf("render template: %v", err)
	}
}

func (app *App) createTodo(writer http.ResponseWriter, request *http.Request) {
	title := strings.TrimSpace(request.FormValue("title"))
	if title == "" || len(title) > 255 {
		http.Error(writer, "Judul wajib diisi dan maksimal 255 karakter", http.StatusBadRequest)
		return
	}

	if _, err := app.db.ExecContext(request.Context(), `INSERT INTO todos (title) VALUES (?)`, title); err != nil {
		http.Error(writer, "Gagal menambahkan todo", http.StatusInternalServerError)
		return
	}
	redirectHome(writer, request)
}

func (app *App) updateTodo(writer http.ResponseWriter, request *http.Request) {
	id, ok := todoID(writer, request)
	if !ok {
		return
	}
	title := strings.TrimSpace(request.FormValue("title"))
	if title == "" || len(title) > 255 {
		http.Error(writer, "Judul wajib diisi dan maksimal 255 karakter", http.StatusBadRequest)
		return
	}

	result, err := app.db.ExecContext(request.Context(), `UPDATE todos SET title = ? WHERE id = ?`, title, id)
	if err != nil {
		http.Error(writer, "Gagal memperbarui todo", http.StatusInternalServerError)
		return
	}
	if !ensureFound(writer, result) {
		return
	}
	redirectHome(writer, request)
}

func (app *App) toggleTodo(writer http.ResponseWriter, request *http.Request) {
	id, ok := todoID(writer, request)
	if !ok {
		return
	}

	result, err := app.db.ExecContext(request.Context(), `UPDATE todos SET completed = NOT completed WHERE id = ?`, id)
	if err != nil {
		http.Error(writer, "Gagal mengubah status todo", http.StatusInternalServerError)
		return
	}
	if !ensureFound(writer, result) {
		return
	}
	redirectHome(writer, request)
}

func (app *App) deleteTodo(writer http.ResponseWriter, request *http.Request) {
	id, ok := todoID(writer, request)
	if !ok {
		return
	}

	result, err := app.db.ExecContext(request.Context(), `DELETE FROM todos WHERE id = ?`, id)
	if err != nil {
		http.Error(writer, "Gagal menghapus todo", http.StatusInternalServerError)
		return
	}
	if !ensureFound(writer, result) {
		return
	}
	redirectHome(writer, request)
}

func todoID(writer http.ResponseWriter, request *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(writer, "ID todo tidak valid", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func ensureFound(writer http.ResponseWriter, result sql.Result) bool {
	affected, err := result.RowsAffected()
	if err != nil {
		http.Error(writer, "Gagal memeriksa perubahan", http.StatusInternalServerError)
		return false
	}
	if affected == 0 {
		http.Error(writer, "Todo tidak ditemukan", http.StatusNotFound)
		return false
	}
	return true
}

func redirectHome(writer http.ResponseWriter, request *http.Request) {
	http.Redirect(writer, request, "/", http.StatusSeeOther)
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(writer, request)
		log.Printf("%s %s %s", request.Method, request.URL.Path, time.Since(started))
	})
}
