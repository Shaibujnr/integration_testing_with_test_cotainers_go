package app

import (
	"context"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	rd "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
	pg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"strconv"
	"testing"
	"time"
)

// NoteRepoTestSuite represents the note repository test suite.
type NoteRepoTestSuite struct {
	suite.Suite
	ctx                context.Context
	db                 *gorm.DB
	pgContainer        *postgres.PostgresContainer
	pgConnectionString string
	rdContainer        *redis.RedisContainer
	rdConnectionString string
	rdClient           *rd.Client
}

func (suite *NoteRepoTestSuite) SetupSuite() {
	suite.ctx = context.Background()
	pgContainer, err := postgres.RunContainer(
		suite.ctx,
		testcontainers.WithImage("postgres:15.3-alpine"),
		postgres.WithDatabase("notesdb"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(5*time.Second)),
	)
	suite.NoError(err)

	connStr, err := pgContainer.ConnectionString(suite.ctx, "sslmode=disable")
	suite.NoError(err)

	db, err := gorm.Open(pg.Open(connStr), &gorm.Config{})
	suite.NoError(err)

	suite.pgContainer = pgContainer
	suite.pgConnectionString = connStr
	suite.db = db

	sqlDB, err := suite.db.DB()
	suite.NoError(err)

	err = sqlDB.Ping()
	suite.NoError(err)

	redisContainer, err := redis.RunContainer(suite.ctx, testcontainers.WithImage("redis:6"))
	suite.NoError(err)
	rdConnStr, err := redisContainer.ConnectionString(suite.ctx)
	suite.NoError(err)

	rdConnOptions, err := rd.ParseURL(rdConnStr)
	suite.NoError(err)

	rdClient := rd.NewClient(rdConnOptions)

	suite.rdContainer = redisContainer
	suite.rdConnectionString = connStr
	suite.rdClient = rdClient

	err = suite.rdClient.Ping(suite.ctx).Err()
	suite.NoError(err)

}

func (suite *NoteRepoTestSuite) TearDownSuite() {
	err := suite.pgContainer.Terminate(suite.ctx)
	suite.NoError(err)

	err = suite.rdContainer.Terminate(suite.ctx)
	suite.NoError(err)
}

func (suite *NoteRepoTestSuite) SetupTest() {
	err := suite.db.AutoMigrate(&Note{})
	suite.NoError(err)
}

func (suite *NoteRepoTestSuite) TearDownTest() {
	suite.db.Exec("DROP TABLE IF EXISTS notes CASCADE;")
	suite.rdClient.FlushAll(suite.ctx)
}

func (suite *NoteRepoTestSuite) BeforeTest(_ string, testName string) {
	if testName == "TestSaveUpdatedNote" || testName == "TestDeleteNote" {
		note := Note{Title: "Test Update", Content: "This note will be inserted now"}
		result := suite.db.Save(&note)
		suite.NoError(result.Error)

		idKey := fmt.Sprintf("notes:%d", note.ID)
		titleKey := fmt.Sprintf("notes:%s", note.Title)
		err := suite.rdClient.HSet(suite.ctx, idKey, "id", note.ID).Err()
		suite.NoError(err)
		err = suite.rdClient.HSet(suite.ctx, idKey, "title", note.Title).Err()
		suite.NoError(err)
		err = suite.rdClient.HSet(suite.ctx, idKey, "content", note.Content).Err()
		suite.NoError(err)
		err = suite.rdClient.HSet(suite.ctx, idKey, "created_at", note.CreatedAt).Err()
		suite.NoError(err)
		err = suite.rdClient.HSet(suite.ctx, idKey, "updated_at", note.UpdatedAt).Err()
		suite.NoError(err)
		err = suite.rdClient.HSet(suite.ctx, titleKey, "id", note.ID).Err()
		suite.NoError(err)
		err = suite.rdClient.HSet(suite.ctx, titleKey, "title", note.Title).Err()
		suite.NoError(err)
		err = suite.rdClient.HSet(suite.ctx, titleKey, "content", note.Content).Err()
		suite.NoError(err)
		err = suite.rdClient.HSet(suite.ctx, titleKey, "created_at", note.CreatedAt).Err()
		suite.NoError(err)
		err = suite.rdClient.HSet(suite.ctx, titleKey, "updated_at", note.UpdatedAt).Err()
		suite.NoError(err)
	}
}

func (suite *NoteRepoTestSuite) TestSaveNewNote() {
	// ensure that the cache is empty
	keys, err := suite.rdClient.Keys(suite.ctx, "*").Result()
	suite.NoError(err)
	suite.Empty(keys)

	// ensure that the postgres database is empty
	var notes []Note
	result := suite.db.Find(&notes)
	suite.NoError(result.Error)
	suite.Empty(notes)

	// create repository and save new note
	repo := NewNoteRepository(suite.db, suite.rdClient)
	newNote := Note{Title: "Testing 123", Content: "This note was just inserted"}
	err = repo.SaveNote(&newNote)
	suite.NoError(err)

	// ensure the cache is still empty
	keys, err = suite.rdClient.Keys(suite.ctx, "*").Result()
	suite.NoError(err)
	suite.Empty(keys)

	// ensure that we have a new note in the database
	result = suite.db.Find(&notes)
	suite.NoError(result.Error)
	suite.Equal(1, len(notes))
	suite.Equal(newNote.ID, notes[0].ID)
	suite.Equal(newNote.Title, notes[0].Title)
	suite.Equal(newNote.Content, notes[0].Content)

}

func (suite *NoteRepoTestSuite) TestSaveUpdatedNote() {
	// ensure that we have a note in the database
	var note Note
	result := suite.db.First(&note)
	suite.NoError(result.Error)
	suite.NotZero(note)

	idKey := fmt.Sprintf("notes:%d", note.ID)
	titleKey := fmt.Sprintf("notes:%s", note.Title)

	// ensure that we have the note cached under its id
	res, err := suite.rdClient.Exists(suite.ctx, idKey).Result()
	suite.NoError(err)
	suite.Greater(res, int64(0))

	// ensure that we have the note cached under its title
	res, err = suite.rdClient.Exists(suite.ctx, titleKey).Result()
	suite.NoError(err)
	suite.Greater(res, int64(0))

	// update the note and save it
	repo := NewNoteRepository(suite.db, suite.rdClient)
	note.Content = "This is the updated note"
	err = repo.SaveNote(&note)
	suite.NoError(err)

	// ensure the cache is invalidated
	res, err = suite.rdClient.Exists(suite.ctx, idKey).Result()
	suite.NoError(err)
	suite.Equal(int64(0), res)
	res, err = suite.rdClient.Exists(suite.ctx, titleKey).Result()
	suite.NoError(err)
	suite.Equal(int64(0), res)

	// ensure the note has been updated in the database.
	var notes []Note
	result = suite.db.Find(&notes)
	suite.NoError(result.Error)
	suite.Equal(1, len(notes))
	suite.Equal(note.ID, notes[0].ID)
	suite.Equal(note.Title, notes[0].Title)
	suite.Equal(note.Content, notes[0].Content)
}

func (suite *NoteRepoTestSuite) TestDeleteNote() {
	// ensure we have a note in the database
	var note Note
	result := suite.db.First(&note)
	suite.NoError(result.Error)
	suite.NotZero(note)

	idKey := fmt.Sprintf("notes:%d", note.ID)
	titleKey := fmt.Sprintf("notes:%s", note.Title)

	// ensure we have the note cached
	res, err := suite.rdClient.Exists(suite.ctx, idKey).Result()
	suite.NoError(err)
	suite.Greater(res, int64(0))
	res, err = suite.rdClient.Exists(suite.ctx, titleKey).Result()
	suite.NoError(err)
	suite.Greater(res, int64(0))

	// delete the note
	repo := NewNoteRepository(suite.db, suite.rdClient)
	err = repo.DeleteNote(int(note.ID))
	suite.NoError(err)

	// ensure that the cache has been cleared
	res, err = suite.rdClient.Exists(suite.ctx, idKey).Result()
	suite.NoError(err)
	suite.Equal(int64(0), res)
	res, err = suite.rdClient.Exists(suite.ctx, titleKey).Result()
	suite.NoError(err)
	suite.Equal(int64(0), res)

	// ensure that the note has been deleted in  postgres
	var notes []Note
	result = suite.db.Find(&notes)
	suite.NoError(result.Error)
	suite.Empty(notes)
}

func (suite *NoteRepoTestSuite) TestGetNote() {
	suite.Run("Get note when note does not exist in cache", func() {
		// empty the notes table and flush the cache
		suite.T().Cleanup(func() {
			suite.db.Exec("DELETE FROM notes;")
			suite.rdClient.FlushAll(suite.ctx)
		})

		// insert a new note in the database
		dbNote := Note{
			Title:   "Testing 123",
			Content: "This is a test content",
		}
		result := suite.db.Save(&dbNote)
		suite.NoError(result.Error)

		// ensure that the cache is empty
		res, err := suite.rdClient.Exists(suite.ctx, fmt.Sprintf("notes:%d", dbNote.ID)).Result()
		suite.NoError(err)
		suite.Equal(int64(0), res)
		res, err = suite.rdClient.Exists(suite.ctx, "notes:Testing 123").Result()
		suite.NoError(err)
		suite.Equal(int64(0), res)

		// get a note by its id
		repo := NewNoteRepository(suite.db, suite.rdClient)
		note := repo.GetNoteById(int(dbNote.ID))
		suite.NotNil(note)

		// ensure that the note is now cached
		res, err = suite.rdClient.Exists(suite.ctx, fmt.Sprintf("notes:%d", dbNote.ID)).Result()
		suite.NoError(err)
		suite.Greater(res, int64(0))

		res, err = suite.rdClient.Exists(suite.ctx, "notes:Testing 123").Result()
		suite.NoError(err)
		suite.Greater(res, int64(0))

		noteMap, err := suite.rdClient.HGetAll(suite.ctx, fmt.Sprintf("notes:%d", dbNote.ID)).Result()
		suite.NoError(err)
		suite.Equal(strconv.Itoa(int(dbNote.ID)), noteMap["id"])
		suite.Equal("Testing 123", noteMap["title"])
		suite.Equal("This is a test content", noteMap["content"])

		noteMap, err = suite.rdClient.HGetAll(suite.ctx, "notes:Testing 123").Result()
		suite.NoError(err)
		suite.Equal(strconv.Itoa(int(dbNote.ID)), noteMap["id"])
		suite.Equal("Testing 123", noteMap["title"])
		suite.Equal("This is a test content", noteMap["content"])
	})
	suite.Run("Get note when note exists in cache", func() {
		// empty the notes table and flush the cache
		suite.T().Cleanup(func() {
			suite.db.Exec("DELETE FROM notes;")
			suite.rdClient.FlushAll(suite.ctx)
		})

		// insert note in database
		dbNote := Note{
			Title:   "Testing 123",
			Content: "This is a test content",
		}
		result := suite.db.Save(&dbNote)
		suite.NoError(result.Error)

		idKey := fmt.Sprintf("notes:%d", dbNote.ID)
		titleKey := fmt.Sprintf("notes:%s", dbNote.Title)

		// cache the note
		suite.rdClient.HSet(suite.ctx, idKey, "id", dbNote.ID)
		suite.rdClient.HSet(suite.ctx, idKey, "title", dbNote.Title)
		suite.rdClient.HSet(suite.ctx, idKey, "content", dbNote.Content)
		suite.rdClient.HSet(suite.ctx, idKey, "created_at", dbNote.CreatedAt)
		suite.rdClient.HSet(suite.ctx, idKey, "updated_at", dbNote.UpdatedAt)
		suite.rdClient.HSet(suite.ctx, titleKey, "id", dbNote.ID)
		suite.rdClient.HSet(suite.ctx, titleKey, "title", dbNote.Title)
		suite.rdClient.HSet(suite.ctx, titleKey, "content", dbNote.Content)
		suite.rdClient.HSet(suite.ctx, titleKey, "created_at", dbNote.CreatedAt)
		suite.rdClient.HSet(suite.ctx, titleKey, "updated_at", dbNote.UpdatedAt)

		// construct mock sql database
		mockDb, mock, err := sqlmock.New()
		suite.NoError(err)
		suite.T().Cleanup(func() {
			mockDb.Close()
		})

		dialector := pg.New(pg.Config{
			Conn:       mockDb,
			DriverName: "postgres",
		})
		db, err := gorm.Open(dialector, &gorm.Config{})
		suite.NoError(err)

		// get the note by id and ensure the note was successfully retrieved
		repo := NewNoteRepository(db, suite.rdClient)
		note := repo.GetNoteById(int(dbNote.ID))
		suite.NotNil(note)
		suite.Equal(dbNote.ID, note.ID)
		suite.Equal(dbNote.Title, note.Title)
		suite.Equal(dbNote.Content, note.Content)

		// ensure all query expectations were met
		// since we didn't set any expectations
		// this would only pass if no query was executed on the database
		// indicating that our repository didn't query the database to get
		// the note.
		err = mock.ExpectationsWereMet()
		suite.NoError(err)

	})
	suite.Run("Get note by title when note does not exist in cache", func() {
		// empty the notes table and flush the cache
		suite.T().Cleanup(func() {
			suite.db.Exec("DELETE FROM notes;")
			suite.rdClient.FlushAll(suite.ctx)
		})

		// insert note in db
		dbNote := Note{
			Title:   "Testing 1234",
			Content: "This is a test content",
		}
		result := suite.db.Save(&dbNote)
		suite.NoError(result.Error)

		// ensure note is not cached
		res, err := suite.rdClient.Exists(suite.ctx, fmt.Sprintf("notes:%d", dbNote.ID)).Result()
		suite.NoError(err)
		suite.Equal(int64(0), res)
		res, err = suite.rdClient.Exists(suite.ctx, "notes:Testing 1234").Result()
		suite.NoError(err)
		suite.Equal(int64(0), res)

		// get a note by its title
		repo := NewNoteRepository(suite.db, suite.rdClient)
		note := repo.GetNoteByTitle(dbNote.Title)
		suite.NotNil(note)

		// ensure the note is now cached
		res, err = suite.rdClient.Exists(suite.ctx, fmt.Sprintf("notes:%d", dbNote.ID)).Result()
		suite.NoError(err)
		suite.Greater(res, int64(0))

		res, err = suite.rdClient.Exists(suite.ctx, "notes:Testing 1234").Result()
		suite.NoError(err)
		suite.Greater(res, int64(0))

		noteMap, err := suite.rdClient.HGetAll(suite.ctx, fmt.Sprintf("notes:%d", dbNote.ID)).Result()
		suite.NoError(err)
		suite.Equal(strconv.Itoa(int(dbNote.ID)), noteMap["id"])
		suite.Equal("Testing 1234", noteMap["title"])
		suite.Equal("This is a test content", noteMap["content"])

		noteMap, err = suite.rdClient.HGetAll(suite.ctx, "notes:Testing 1234").Result()
		suite.NoError(err)
		suite.Equal(strconv.Itoa(int(dbNote.ID)), noteMap["id"])
		suite.Equal("Testing 1234", noteMap["title"])
		suite.Equal("This is a test content", noteMap["content"])
	})
	suite.Run("Get note by title when note exists in cache", func() {
		// empty the notes table and flush the cache
		suite.T().Cleanup(func() {
			suite.db.Exec("DELETE FROM notes;")
			suite.rdClient.FlushAll(suite.ctx)
		})
		dbNote := Note{
			Title:   "Testing 123",
			Content: "This is a test content",
		}
		result := suite.db.Save(&dbNote)
		suite.NoError(result.Error)

		idKey := fmt.Sprintf("notes:%d", dbNote.ID)
		titleKey := fmt.Sprintf("notes:%s", dbNote.Title)

		// store note in cache
		suite.rdClient.HSet(suite.ctx, idKey, "id", dbNote.ID)
		suite.rdClient.HSet(suite.ctx, idKey, "title", dbNote.Title)
		suite.rdClient.HSet(suite.ctx, idKey, "content", dbNote.Content)
		suite.rdClient.HSet(suite.ctx, idKey, "created_at", dbNote.CreatedAt)
		suite.rdClient.HSet(suite.ctx, idKey, "updated_at", dbNote.UpdatedAt)
		suite.rdClient.HSet(suite.ctx, titleKey, "id", dbNote.ID)
		suite.rdClient.HSet(suite.ctx, titleKey, "title", dbNote.Title)
		suite.rdClient.HSet(suite.ctx, titleKey, "content", dbNote.Content)
		suite.rdClient.HSet(suite.ctx, titleKey, "created_at", dbNote.CreatedAt)
		suite.rdClient.HSet(suite.ctx, titleKey, "updated_at", dbNote.UpdatedAt)

		// setup database mock
		mockDb, mock, err := sqlmock.New()
		suite.NoError(err)
		suite.T().Cleanup(func() {
			mockDb.Close()
		})

		dialector := pg.New(pg.Config{
			Conn:       mockDb,
			DriverName: "postgres",
		})
		db, err := gorm.Open(dialector, &gorm.Config{})
		suite.NoError(err)

		repo := NewNoteRepository(db, suite.rdClient)
		note := repo.GetNoteByTitle(dbNote.Title)
		suite.NotNil(note)
		suite.Equal(dbNote.ID, note.ID)
		suite.Equal(dbNote.Title, note.Title)
		suite.Equal(dbNote.Content, note.Content)

		err = mock.ExpectationsWereMet()
		suite.NoError(err)
	})
}

func TestNoteRepository(t *testing.T) {
	suite.Run(t, new(NoteRepoTestSuite))
}
