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
	DuplicateNoteError      = errors.New("note with same title already exists")
	SomethingWentWrongError = errors.New("something went wrong")
	NoteNotFoundError       = errors.New("note not found")
)

type Note struct {
	gorm.Model
	Title   string `gorm:"column:title;not null;unique"`
	Content string `gorm:"column:content;not null"`
}

type NoteRepositoryInterface interface {
	SaveNote(note *Note) error
	GetNoteById(id int) *Note
	GetNoteByTitle(title string) *Note
	DeleteNote(id int) error
}

type NoteRepository struct {
	db    *gorm.DB
	redis *redis.Client
}

func NewNoteRepository(db *gorm.DB, rd *redis.Client) *NoteRepository {
	return &NoteRepository{
		db:    db,
		redis: rd,
	}
}

func (repo *NoteRepository) convertMapToNote(noteMap map[string]string) (Note, error) {
	noteID, err := strconv.Atoi(noteMap["id"])
	if err != nil {
		return Note{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, noteMap["created_at"])
	if err != nil {
		return Note{}, err
	}

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

type Application struct {
	noteRepository NoteRepositoryInterface
}

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

func (app *Application) GetNoteById(id int) (Note, error) {
	note := app.noteRepository.GetNoteById(id)
	if note == nil {
		return Note{}, NoteNotFoundError
	}
	return *note, nil
}

func (app *Application) DeleteNote(id int) error {
	note := app.noteRepository.GetNoteById(id)
	if note == nil {
		return NoteNotFoundError
	}
	return app.noteRepository.DeleteNote(id)
}
