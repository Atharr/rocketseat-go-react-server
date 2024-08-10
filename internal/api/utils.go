package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Atharr/rocketseat-go-react-server/internal/store/pgstore"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h apiHandler) readRoom(w http.ResponseWriter, r *http.Request) (room pgstore.Room,
	rawRoomID string, roomID uuid.UUID, ok bool) {
	rawRoomID = chi.URLParam(r, "room_id")
	roomID, err := uuid.Parse(rawRoomID)
	if err != nil {
		http.Error(w, MsgInvalidRoomID, http.StatusBadRequest)
		return pgstore.Room{}, "", uuid.UUID{}, false
	}

	room, err = h.q.GetRoom(r.Context(), roomID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, MsgRoomNotFound, http.StatusBadRequest)
			return pgstore.Room{}, "", uuid.UUID{}, false
		}

		slog.Error(MsgFailedToGetRoom, "error", err)
		http.Error(w, MsgSomethingWentWrong, http.StatusInternalServerError)
		return pgstore.Room{}, "", uuid.UUID{}, false
	}

	return room, rawRoomID, roomID, true
}

func sendJSON(w http.ResponseWriter, rawData any) {
	data, _ := json.Marshal(rawData)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
