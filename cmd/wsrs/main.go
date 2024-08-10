package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/Atharr/rocketseat-go-react-server/internal/api"
	"github.com/Atharr/rocketseat-go-react-server/internal/store/pgstore"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file: ", err)
	}

	pool, err := pgxpool.New(context.Background(), fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s",
		os.Getenv("WSRS_DATABASE_HOST"),
		os.Getenv("WSRS_DATABASE_PORT"),
		os.Getenv("WSRS_DATABASE_USER"),
		os.Getenv("WSRS_DATABASE_PASSWORD"),
		os.Getenv("WSRS_DATABASE_NAME"),
	))
	if err != nil {
		log.Fatal(err)
	}

	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		log.Fatal(err)
	}

	handler := api.NewHandler(pgstore.New(pool))

	go func() {
		if err := http.ListenAndServe(":8080", handler); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit
}
