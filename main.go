package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/thedevsaddam/renderer"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

var (
	rnd    *renderer.Render
	client *mongo.Client
	db     *mongo.Database
)

const (
	dbName         string = "golang-todo"
	collectionName string = "todo"
)

type (
	// struct to db model
	TodoModel struct {
		ID        primitive.ObjectID `bson:"id,omitempty"`
		Title     string             `bson:"title"`
		Completed bool               `bson:"completed"`
		CreatedAt time.Time          `bson:"created_at"`
	}
	// that the Frontend will display
	Todo struct {
		ID        string    `json:"id"`
		Title     string    `json:"title"`
		Completed bool      `json:"completed"`
		CreatedAt time.Time `json:"created_at"`
	}
	// the structure of the JSON response data returned
	GetTodoResponse struct {
		Message string `json:"message"`
		Data    []Todo `json:"data"`
	}
	// create todo
	CreateTodo struct {
		Title string `json:"title"`
	}
	// update todo
	UpdateTodo struct {
		Title     string `json:"title"`
		Completed bool   `json:"completed"`
	}
)

func init() {
	fmt.Println("init func running")

	rnd = renderer.New(
		renderer.Options{
			/* This option allows us to look for files inside the HTML folder
			with the “.html” extension and render them as templates.*/
			ParseGlobPattern: "html/*.html", // HTML parsing option
		},
	)
	var err error

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err = mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	checkError(err)

	err = client.Ping(ctx, readpref.Primary())
	checkError(err)

	db = client.Database(dbName)
}

func homeHandler(rw http.ResponseWriter, r *http.Request) {
	// filePath := "./README.md"
	// FileView - renders the readme file
	// err := rnd.FileView(rw, http.StatusOK, filePath, "readme.md")

	// it returns the indexPage in the HTML template.
	err := rnd.HTML(rw, http.StatusOK, "indexPage", nil)
	checkError(err)
}

func main() {
	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Get("/", homeHandler)
	router.Mount("/todo", todoHandlers())

	// Serve static files
	// http.FileServer to serve static files from the 'static' directory on the server
	fs := http.FileServer(http.Dir("./static"))
	router.Handle("/static/*", http.StripPrefix("/static/", fs))

	server := &http.Server{
		Addr:         ":9000",
		Handler:      router,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	// create a channel to receive siglan
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// start the server in a goroutine
	go func() {
		fmt.Println("Server started on port", 9000)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("listen:%s\n", err)
		}
	}()

	// wait for a signal to shut down the server
	sig := <-stopChan
	log.Printf("signal received: %v\n", sig)

	// disconnect mongo client from the database
	if err := client.Disconnect(context.Background()); err != nil {
		panic(err)
	}
	// create a context with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// shutdown the server gracefully
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v\n", err)
	}
	log.Println("Server shutdown gracefully")

}

// todoHandlers ...
func todoHandlers() http.Handler {
	router := chi.NewRouter()
	router.Group(
		func(r chi.Router) {
			r.Get("/", getTodos)
			r.Post("/", createTodo)
			r.Put("/{id}", updateTodo)
			r.Delete("/{id}", deleteTodo)
		})

	return router
}

// getTodos ...
func getTodos(rw http.ResponseWriter, r *http.Request) {
	var todoListFromDB = []TodoModel{}
	filter := bson.D{}

	cursor, err := db.Collection(collectionName).Find(context.Background(), filter)

	if err != nil {
		log.Printf("failed to fetch todo records from the db: %v\n", err)
		rnd.JSON(rw, http.StatusBadRequest, renderer.M{
			"message": "Could not fetch the todo collection",
			"error":   err.Error(),
		})

		return
	}

	todoList := []Todo{}
	if err := cursor.All(context.Background(), &todoListFromDB); err != nil {
		checkError(err)
	}

	// loop through the database list, convert TodoModel to JSON and append to the todoList array.
	for _, td := range todoListFromDB {
		todoList = append(todoList, Todo{
			ID:        td.ID.Hex(),
			Title:     td.Title,
			Completed: td.Completed,
			CreatedAt: td.CreatedAt,
		})
	}
	rnd.JSON(rw, http.StatusOK, GetTodoResponse{
		Message: "All todos retrieved",
		Data:    todoList,
	})
}

// createTodo ...
func createTodo(rw http.ResponseWriter, r *http.Request) {
	var todoReq CreateTodo
	if err := json.NewDecoder(r.Body).Decode(&todoReq); err != nil {
		log.Printf("failed to decode json data: %v\n", err.Error())
		rnd.JSON(rw, http.StatusBadRequest, renderer.M{
			"message": "could not decode data",
		})
		return
	}

	if todoReq.Title == "" {
		log.Printf("no title added to response body")
		rnd.JSON(rw, http.StatusBadRequest, renderer.M{
			"message": "please add a title",
		})
		return
	}

	todoModel := TodoModel{
		ID:        primitive.NewObjectID(),
		Title:     todoReq.Title,
		Completed: false,
		CreatedAt: time.Now(),
	}

	// add the todo to the db
	data, err := db.Collection(collectionName).InsertOne(r.Context(), todoModel)
	if err != nil {
		log.Printf("failed to insert data into the db: %v\n", err.Error())
		rnd.JSON(rw, http.StatusInternalServerError, renderer.M{
			"message": "Failed to insert data into db",
			"error":   err.Error(),
		})
		return
	}
	rnd.JSON(rw, http.StatusCreated, renderer.M{
		"message": "Todo created successfully",
		"ID":      data.InsertedID,
	})
}

// updateTodo
func updateTodo(rw http.ResponseWriter, r *http.Request) {
	// get the id from the url params
	id := strings.TrimSpace(chi.URLParam(r, "id"))

	res, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		log.Printf("the id param is not a valid a hex value: %v\n", err.Error())
		rnd.JSON(rw, http.StatusInternalServerError, renderer.M{
			"message": "The id is Invalid",
			"error":   err.Error(),
		})
		return
	}

	// store the user input sent through the request body
	var updateTodoReq UpdateTodo

	if err := json.NewDecoder(r.Body).Decode(&updateTodoReq); err != nil {
		log.Printf("failed to decode the json response body data: %v\n", err.Error())
		rnd.JSON(rw, http.StatusBadRequest, err.Error())
	}
	if updateTodoReq.Title == "" {
		rnd.JSON(rw, http.StatusBadRequest, renderer.M{
			"message": "Title connot be empty",
		})
		return
	}

	// update the todo in the db
	filter := bson.M{"id": res}
	update := bson.M{"$set": bson.M{"title": updateTodoReq.Title, "completed": updateTodoReq.Completed}}
	data, err := db.Collection(collectionName).UpdateOne(r.Context(), filter, update)

	if err != nil {
		log.Printf("failed to update db collection: %v\n", err.Error())
		rnd.JSON(rw, http.StatusInternalServerError, renderer.M{
			"message": "Failed to update data in the db",
			"error":   err.Error(),
		})
		return
	}
	rnd.JSON(rw, http.StatusOK, renderer.M{
		"message": "Todo updated successfully",
		"data":    data.ModifiedCount,
	})
}

// deleteTodo ...
func deleteTodo(rw http.ResponseWriter, r *http.Request) {
	// get the id from the url params
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	res, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		log.Printf("invalid id: %v\n", err.Error())
		rnd.JSON(rw, http.StatusBadRequest, err.Error())
		return
	}

	filter := bson.M{"id": res}
	if data, err := db.Collection(collectionName).DeleteOne(r.Context(), filter); err != nil {
		log.Printf("could not delete item from database: %v\n", err.Error())
		rnd.JSON(rw, http.StatusInternalServerError, renderer.M{
			"message": "an error occured while deleting todo item",
			"error":   err.Error(),
		})
	} else {
		rnd.JSON(rw, http.StatusOK, renderer.M{
			"message": "item deleted successfully",
			"data":    data,
		})
	}
}

// checkError ...
func checkError(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
