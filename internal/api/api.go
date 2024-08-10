package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/Atharr/rocketseat-go-react-server/internal/store/pgstore"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
)

type apiHandler struct {
	q           *pgstore.Queries
	r           *chi.Mux
	upgrader    websocket.Upgrader
	subscribers map[string]map[*websocket.Conn]context.CancelFunc
	mu          *sync.Mutex
}

type Message struct {
	Kind   string `json:"kind"`
	Value  any    `json:"value"`
	RoomID string `json:"-"`
}

type MessageMessageCreated struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type MessageMessageReactionCount struct {
	ID    string `json:"id"`
	Count int64  `json:"count"`
}

const (
	MsgFailedToGetMessage        = "failed to get message"
	MsgFailedToGetRoom           = "failed to get room"
	MsgFailedToGetRoomMessages   = "failed to get room messages"
	MsgFailedToGetRooms          = "failed to get rooms"
	MsgFailedToInsertMessage     = "failed to insert message"
	MsgFailedToInsertRoom        = "failed to insert room"
	MsgFailedToReactToMessage    = "failed to react to message"
	MsgFailedToSendMessage       = "failed to send message to client"
	MsgFailedToUpgradeConnection = "failed to upgrade to websocket connection"
	MsgInvalidJSON               = "invalid json"
	MsgInvalidMessageID          = "invalid message id"
	MsgInvalidRoomID             = "invalid room id"
	MsgMessageNotFound           = "message not found"
	MsgNewClientConnected        = "new client connected"
	MsgRoomNotFound              = "room not found"
	MsgSomethingWentWrong        = "something went wrong"
)

const (
	MessageKindMessageCreated          = "message_created"
	MessageKindMessageRactionIncreased = "message_reaction_increased"
	MessageKindMessageRactionDecreased = "message_reaction_decreased"
	MessageKindMessageAnswered         = "message_answered"
)

func NewHandler(q *pgstore.Queries) http.Handler {
	a := apiHandler{
		q:           q,
		upgrader:    websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		subscribers: make(map[string]map[*websocket.Conn]context.CancelFunc),
		mu:          &sync.Mutex{},
	}
	r := chi.NewRouter()

	r.Use(middleware.RequestID, middleware.Recoverer, middleware.Logger)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	}))

	r.Get("/subscribe/{room_id}", a.handleSubscribe)

	r.Route("/api", func(r chi.Router) {
		r.Route("/rooms", func(r chi.Router) {
			r.Get("/", a.handleGetRooms)
			r.Post("/", a.handleCreateRoom)

			r.Route("/{room_id}", func(r chi.Router) {
				r.Get("/", a.handleGetRoom)

				r.Route("/messages", func(r chi.Router) {
					r.Get("/", a.handleGetRoomMessages)
					r.Post("/", a.handleCreateRoomMessage)

					r.Route("/{message_id}", func(r chi.Router) {
						r.Get("/", a.handleGetRoomMessage)
						r.Patch("/react", a.handleReactToMessage)
						r.Delete("/react", a.handleRemoveReactFromMessage)
						r.Patch("/answer", a.handleMarkMessageAsAnswered)
					})
				})
			})
		})
	})

	a.r = r
	return a
}

func (h apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.r.ServeHTTP(w, r)
}

func (h apiHandler) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	_, rawRoomID, _, ok := h.readRoom(w, r)
	if !ok {
		return
	}

	// Upgrade the connection to websocket
	c, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn(MsgFailedToUpgradeConnection, "error", err)
		http.Error(w, MsgFailedToUpgradeConnection, http.StatusBadRequest)
		return
	}
	defer c.Close()

	// Add the connection to the room
	ctx, cancel := context.WithCancel(r.Context())
	h.mu.Lock()
	if _, ok := h.subscribers[rawRoomID]; !ok {
		h.subscribers[rawRoomID] = make(map[*websocket.Conn]context.CancelFunc)
	}
	slog.Info(MsgNewClientConnected, "room_id", rawRoomID, "client_ip", r.RemoteAddr)
	h.subscribers[rawRoomID][c] = cancel
	h.mu.Unlock()
	<-ctx.Done()

	// Remove the connection from the room
	h.mu.Lock()
	delete(h.subscribers[rawRoomID], c)
	h.mu.Unlock()
}

func (h apiHandler) notifyClients(msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()

	subcribers, ok := h.subscribers[msg.RoomID]
	if !ok || len(subcribers) == 0 {
		return
	}

	for conn, cancel := range subcribers {
		if err := conn.WriteJSON(msg); err != nil {
			slog.Error(MsgFailedToSendMessage, "error", err)
			cancel()
		}
	}
}

func (h apiHandler) handleGetRooms(w http.ResponseWriter, r *http.Request) {
	rooms, err := h.q.GetRooms(r.Context())
	if err != nil {
		slog.Error(MsgFailedToGetRooms, "error", err)
		http.Error(w, MsgSomethingWentWrong, http.StatusInternalServerError)
		return
	}

	if rooms == nil {
		rooms = []pgstore.Room{}
	}
	sendJSON(w, rooms)
}

func (h apiHandler) handleGetRoom(w http.ResponseWriter, r *http.Request) {
	room, _, _, ok := h.readRoom(w, r)
	if !ok {
		return
	}

	sendJSON(w, room)
}

func (h apiHandler) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	type _body struct {
		Theme string `json:"theme"`
	}
	var body _body
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, MsgInvalidJSON, http.StatusBadRequest)
		return
	}

	roomID, err := h.q.InsertRoom(r.Context(), body.Theme)
	if err != nil {
		slog.Error(MsgFailedToInsertRoom, "error", err)
		http.Error(w, MsgSomethingWentWrong, http.StatusInternalServerError)
		return
	}

	type response struct {
		ID string `json:"id"`
	}

	sendJSON(w, response{ID: roomID.String()})
}

func (h apiHandler) handleGetRoomMessages(w http.ResponseWriter, r *http.Request) {
	_, _, roomID, ok := h.readRoom(w, r)
	if !ok {
		return
	}

	messages, err := h.q.GetRoomMessages(r.Context(), roomID)
	if err != nil {
		slog.Error(MsgFailedToGetRoomMessages, "error", err)
		http.Error(w, MsgSomethingWentWrong, http.StatusInternalServerError)
		return
	}

	if messages == nil {
		messages = []pgstore.Message{}
	}
	sendJSON(w, messages)
}

func (h apiHandler) handleCreateRoomMessage(w http.ResponseWriter, r *http.Request) {
	_, rawRoomID, roomID, ok := h.readRoom(w, r)
	if !ok {
		return
	}

	type _body struct {
		Message string `json:"message"`
	}
	var body _body
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, MsgInvalidJSON, http.StatusBadRequest)
		return
	}

	messageID, err := h.q.InsertMessage(r.Context(), pgstore.InsertMessageParams{
		RoomID:  roomID,
		Message: body.Message,
	})
	if err != nil {
		slog.Error(MsgFailedToInsertMessage, "error", err)
		http.Error(w, MsgSomethingWentWrong, http.StatusInternalServerError)
		return
	}

	type response struct {
		ID string `json:"id"`
	}

	sendJSON(w, response{ID: messageID.String()})

	go h.notifyClients(Message{
		Kind:   MessageKindMessageCreated,
		RoomID: rawRoomID,
		Value: MessageMessageCreated{
			ID:      messageID.String(),
			Message: body.Message,
		},
	})
}

func (h apiHandler) handleGetRoomMessage(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := h.readRoom(w, r)
	if !ok {
		return
	}

	rawMessageID := chi.URLParam(r, "message_id")
	messageID, err := uuid.Parse(rawMessageID)
	if err != nil {
		http.Error(w, MsgInvalidMessageID, http.StatusBadRequest)
		return
	}

	message, err := h.q.GetMessage(r.Context(), messageID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, MsgMessageNotFound, http.StatusNotFound)
			return
		}
		slog.Error(MsgFailedToGetMessage, "error", err)
		http.Error(w, MsgSomethingWentWrong, http.StatusInternalServerError)
		return
	}

	sendJSON(w, message)
}

func (h apiHandler) handleReactToMessage(w http.ResponseWriter, r *http.Request) {
	_, rawRoomID, _, ok := h.readRoom(w, r)
	if !ok {
		return
	}

	rawMessageID := chi.URLParam(r, "message_id")
	messageID, err := uuid.Parse(rawMessageID)
	if err != nil {
		http.Error(w, MsgInvalidMessageID, http.StatusBadRequest)
		return
	}

	count, err := h.q.ReactToMessage(r.Context(), messageID)
	if err != nil {
		slog.Error(MsgFailedToReactToMessage, "error", err)
		http.Error(w, MsgSomethingWentWrong, http.StatusInternalServerError)
		return
	}

	type response struct {
		Count int64 `json:"count"`
	}
	sendJSON(w, response{Count: count})

	go h.notifyClients(Message{
		Kind:   MessageKindMessageRactionIncreased,
		RoomID: rawRoomID,
		Value: MessageMessageReactionCount{
			ID:    rawMessageID,
			Count: count,
		},
	})
}

func (h apiHandler) handleRemoveReactFromMessage(w http.ResponseWriter, r *http.Request) {

}

func (h apiHandler) handleMarkMessageAsAnswered(w http.ResponseWriter, r *http.Request) {

}
