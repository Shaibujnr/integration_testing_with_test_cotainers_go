package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"log/slog"
	"strconv"
	"time"
)

var (
	// DuplicateNoteError is returned when adding a note with a title that conflicts
	// with that of an existing note
	DuplicateNoteError = errors.New("note with same title already exists")
	// SomethingWentWrongError is returned when the code returns an error we are not expecting
	SomethingWentWrongError = errors.New("something went wrong")
	// NoteNotFoundError is returned when a note is not found
	NoteNotFoundError = errors.New("note not found")
)

// Note represents a note that has a title and the note content
type Note struct {
	gorm.Model
	// Title is the title of the note.
	Title string `gorm:"column:title;not null;unique"`
	// Content is the content of the note.
	Content string `gorm:"column:content;not null"`
}

// NoteRepositoryInterface is the interface for the note repository
type NoteRepositoryInterface interface {
	SaveNote(note *Note) error
	GetNoteById(id int) *Note
	GetNoteByTitle(title string) *Note
	DeleteNote(id int) error
}

// NoteRepository implements the NoteRepositoryInterface
type NoteRepository struct {
	db    *gorm.DB
	redis *redis.Client
}

// NewNoteRepository is the factory function to create a new NoteRepository
// Parameters:
// -  db: gorm database client
// -  rd: redis client
//
// Returns:
// - *NoteRepository: A pointer to the newly created NoteRepository
func NewNoteRepository(db *gorm.DB, rd *redis.Client) *NoteRepository {
	return &NoteRepository{
		db:    db,
		redis: rd,
	}
}

// convertMapToNote will convert a map[string]string to a Note object
// Parameters:
// -    noteMap: map[string]string that holds the note data
// Returns:
// - Note: the resulting note object
// - error: any error that arises from this conversion
func (repo *NoteRepository) convertMapToNote(noteMap map[string]string) (Note, error) {
	// convert the id from string to integer
	noteID, err := strconv.Atoi(noteMap["id"])
	if err != nil {
		return Note{}, err
	}
	// parse the created_at time string
	createdAt, err := time.Parse(time.RFC3339Nano, noteMap["created_at"])
	if err != nil {
		return Note{}, err
	}
	// parse the updated_at time string
	updatedAt, err := time.Parse(time.RFC3339Nano, noteMap["updated_at"])
	if err != nil {
		return Note{}, err
	}

	return Note{
		Model: gorm.Model{
			ID:        uint(noteID),
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
		Title:   noteMap["title"],
		Content: noteMap["content"],
	}, nil
}

// getNoteFromCache will get the note from the redis cache using the id
func (repo *NoteRepository) getNoteFromCache(id int) *Note {
	result := repo.redis.HGetAll(context.Background(), fmt.Sprintf("notes:%d", id)).Val()
	if len(result) == 0 {
		return nil
	}
	note, err := repo.convertMapToNote(result)
	if err != nil {
		panic(err)
	}
	return &note
}

// getNoteByTitleFromCache will get the note from the redis cache using the title
func (repo *NoteRepository) getNoteByTitleFromCache(title string) *Note {
	result := repo.redis.HGetAll(context.Background(), fmt.Sprintf("notes:%s", title)).Val()
	if len(result) == 0 {
		return nil
	}
	note, err := repo.convertMapToNote(result)
	if err != nil {
		panic(err)
	}
	return &note
}

// deleteFromCache will delete the note from redis by
// deleting the entry stored under the notes id and the
// entry stored under the notes title.
func (repo *NoteRepository) deleteFromCache(note Note) error {
	keysToDelete := make([]string, 0)
	if note.ID > 0 {
		keysToDelete = append(keysToDelete, fmt.Sprintf("notes:%d", note.ID))
	}
	if note.Title != "" {
		keysToDelete = append(keysToDelete, fmt.Sprintf("notes:%s", note.Title))
	}
	return repo.redis.Del(context.Background(), keysToDelete...).Err()
}

// cacheNote will store the note in redis using its id
// as well as it's title
func (repo *NoteRepository) cacheNote(note Note) error {
	idHashKey := fmt.Sprintf("notes:%d", note.ID)
	titleHashKey := fmt.Sprintf("notes:%s", note.Title)
	noteMap := map[string]any{
		"id":         note.ID,
		"title":      note.Title,
		"content":    note.Content,
		"created_at": note.CreatedAt,
		"updated_at": note.UpdatedAt,
	}
	for key, val := range noteMap {
		err := repo.redis.HSet(context.Background(), idHashKey, key, val).Err()
		if err != nil {
			return err
		}
		err = repo.redis.HSet(context.Background(), titleHashKey, key, val).Err()
		if err != nil {
			return err
		}
	}
	return nil
}

// SaveNote will store the note in the postgres database.
// This would also invalidate the cache to ensure the next
// read will update the cache with the latest data
func (repo *NoteRepository) SaveNote(note *Note) error {
	err := repo.deleteFromCache(*note)
	if err != nil {
		return err
	}
	result := repo.db.Save(note)
	if result.Error != nil {
		return result.Error
	}
	return nil
}

// GetNoteById will attempt to retrieve the note from the
// redis cache by its id, if it doesn't find the note in redis
// it will get it from postgres and store it in the cache
// before returning it to the caller.
func (repo *NoteRepository) GetNoteById(id int) *Note {
	cachedNote := repo.getNoteFromCache(id)
	if cachedNote != nil {
		return cachedNote
	}
	note := Note{Model: gorm.Model{ID: uint(id)}}
	result := repo.db.First(&note)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil
		}
		panic(result.Error)
	}
	err := repo.cacheNote(note)
	if err != nil {
		panic(err)
	}
	return &note
}

// GetNoteByTitle will attempt to retrieve the note from the
// redis cache by its title, if it doesn't find the note in redis
// it will get it from postgres and store it in the cache
// before returning it to the caller.
func (repo *NoteRepository) GetNoteByTitle(title string) *Note {
	cachedNote := repo.getNoteByTitleFromCache(title)
	if cachedNote != nil {
		return cachedNote
	}
	note := Note{Title: title}
	result := repo.db.First(&note)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil
		}
		panic(result.Error)
	}
	err := repo.cacheNote(note)
	if err != nil {
		panic(err)
	}
	return &note
}

// DeleteNote will delete the note from the cache first and
// then postgres.
func (repo *NoteRepository) DeleteNote(id int) error {
	cachedNote := repo.getNoteFromCache(id)
	if cachedNote != nil {
		err := repo.deleteFromCache(*cachedNote)
		if err != nil {
			return err
		}
	}
	result := repo.db.Delete(&Note{}, id)
	return result.Error
}

// Application represents the application class
type Application struct {
	noteRepository NoteRepositoryInterface
}

// CreateNote is the application use case method to create a new note.
func (app *Application) CreateNote(title string, content string) (Note, error) {
	existingNote := app.noteRepository.GetNoteByTitle(title)
	if existingNote != nil {
		return Note{}, DuplicateNoteError
	}
	note := &Note{Title: title, Content: content}
	if err := app.noteRepository.SaveNote(note); err != nil {
		return Note{}, SomethingWentWrongError
	}
	return *note, nil
}

// UpdateNote is the application use case method to update an existing note.
func (app *Application) UpdateNote(id int, content string) (Note, error) {
	note := app.noteRepository.GetNoteById(id)
	if note == nil {
		return Note{}, NoteNotFoundError
	}
	note.Content = content
	if err := app.noteRepository.SaveNote(note); err != nil {
		slog.Error("Error in saving note", "error", err.Error())
		return Note{}, SomethingWentWrongError
	}
	return *note, nil
}

// GetNoteById is the application use case method to get a note by its id.
func (app *Application) GetNoteById(id int) (Note, error) {
	note := app.noteRepository.GetNoteById(id)
	if note == nil {
		return Note{}, NoteNotFoundError
	}
	return *note, nil
}

// DeleteNote is the application use case method to delete a note.
func (app *Application) DeleteNote(id int) error {
	note := app.noteRepository.GetNoteById(id)
	if note == nil {
		return NoteNotFoundError
	}
	return app.noteRepository.DeleteNote(id)
}
