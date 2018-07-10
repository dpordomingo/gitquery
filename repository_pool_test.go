package gitbase

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/src-d/go-git-fixtures.v3"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
	"gopkg.in/src-d/go-mysql-server.v0/sql"
)

func TestRepository(t *testing.T) {
	require := require.New(t)

	gitRepo := &git.Repository{}
	repo := NewRepository("identifier", gitRepo)

	require.Equal("identifier", repo.ID)
	require.Equal(gitRepo, repo.Repo)

	repo = NewRepository("/other/path", nil)

	require.Equal("/other/path", repo.ID)
	require.Nil(repo.Repo)
}

func TestRepositoryPoolBasic(t *testing.T) {
	require := require.New(t)

	pool := NewRepositoryPool()

	repo, err := pool.GetPos(0)
	require.Nil(repo)
	require.Equal(io.EOF, err)

	repo, err = pool.GetRepo("foo")
	require.Nil(repo)
	require.EqualError(err, ErrPoolRepoNotFound.New("foo").Error())

	pool.Add(gitRepo("0", "/directory/should/not/exist"))
	repo, err = pool.GetPos(0)
	require.Nil(repo)
	require.EqualError(err, git.ErrRepositoryNotExists.Error())

	_, err = pool.GetPos(1)
	require.Equal(io.EOF, err)

	path := fixtures.Basic().ByTag("worktree").One().Worktree().Root()

	err = pool.Add(gitRepo("1", path))
	require.NoError(err)

	repo, err = pool.GetPos(1)
	require.NoError(err)
	require.Equal("1", repo.ID)
	require.NotNil(repo.Repo)

	repo, err = pool.GetRepo("1")
	require.NoError(err)
	require.Equal("1", repo.ID)
	require.NotNil(repo.Repo)

	err = pool.Add(gitRepo("1", path))
	require.Error(err)
	require.True(errRepoAlreadyRegistered.Is(err))

	_, err = pool.GetPos(0)
	require.Equal(git.ErrRepositoryNotExists, err)

	_, err = pool.GetPos(2)
	require.Equal(io.EOF, err)
}

func TestRepositoryPoolGit(t *testing.T) {
	require := require.New(t)

	path := fixtures.Basic().ByTag("worktree").One().Worktree().Root()

	pool := NewRepositoryPool()

	_, err := pool.AddGit("/do/not/exist")
	require.Error(err)
	require.True(errRepoCannotOpen.Is(err))

	id, err := pool.AddGit(path)
	require.Equal(path, id)
	require.NoError(err)

	repo, err := pool.GetPos(0)
	require.Equal(path, repo.ID)
	require.NotNil(repo.Repo)
	require.NoError(err)

	iter, err := repo.Repo.CommitObjects()
	require.NoError(err)

	count := 0

	for {
		commit, err := iter.Next()
		if err != nil {
			break
		}

		require.NotNil(commit)

		count++
	}

	require.Equal(9, count)
}

func TestRepositoryPoolIterator(t *testing.T) {
	require := require.New(t)

	path := fixtures.Basic().ByTag("worktree").One().Worktree().Root()

	pool := NewRepositoryPool()
	pool.Add(gitRepo("0", path))
	pool.Add(gitRepo("1", path))

	iter, err := pool.RepoIter()
	require.NoError(err)

	count := 0

	for {
		repo, err := iter.Next()
		if err != nil {
			require.Equal(io.EOF, err)
			break
		}

		require.NotNil(repo)
		require.Equal(strconv.Itoa(count), repo.ID)

		count++
	}

	require.Equal(2, count)
}

type testCommitIter struct {
	iter   object.CommitIter
	repoID string
}

func (d *testCommitIter) NewIterator(
	repo *Repository,
) (RowRepoIter, error) {
	iter, err := repo.Repo.CommitObjects()
	if err != nil {
		return nil, err
	}

	return &testCommitIter{iter: iter, repoID: repo.ID}, nil
}

func (d *testCommitIter) Next() (sql.Row, error) {
	commit, err := d.iter.Next()
	if err != nil {
		return nil, err
	}

	return commitToRow(d.repoID, commit), nil
}

func (d *testCommitIter) Close() error {
	if d.iter != nil {
		d.iter.Close()
	}

	return nil
}

func testRepoIter(num int, require *require.Assertions, ctx *sql.Context) {
	cIter := &testCommitIter{}

	repoIter, err := NewRowRepoIter(ctx, cIter)
	require.NoError(err)

	count := 0
	for {
		row, err := repoIter.Next()
		if err != nil {
			require.Equal(io.EOF, err)
			break
		}

		require.NotNil(row)

		count++
	}

	// 9 is the number of commits from the test repo
	require.Equal(9*num, count)
}

func TestRepositoryRowIterator(t *testing.T) {
	require := require.New(t)

	path := fixtures.Basic().ByTag("worktree").One().Worktree().Root()

	pool := NewRepositoryPool()
	session := NewSession(pool)
	ctx := sql.NewContext(context.TODO(), sql.WithSession(session))
	max := 64

	for i := 0; i < max; i++ {
		pool.Add(gitRepo(strconv.Itoa(i), path))
	}

	testRepoIter(max, require, ctx)

	// Test multiple iterators at the same time

	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			testRepoIter(max, require, ctx)
			wg.Done()
		}()
	}

	wg.Wait()
}

func TestRepositoryPoolAddDir(t *testing.T) {
	require := require.New(t)

	tmpDir, err := ioutil.TempDir("", "gitbase-test")
	require.NoError(err)

	tmpDirLen := len(SplitPath(tmpDir))
	max := 64

	for i := 0; i < max; i++ {
		orig := fixtures.Basic().ByTag("worktree").One().Worktree().Root()
		p := filepath.Join(tmpDir, strconv.Itoa(i))

		err := os.Rename(orig, p)
		require.NoError(err)
	}

	pool := NewRepositoryPool()
	err = pool.AddDir(tmpDirLen, tmpDir)
	require.NoError(err)

	require.Equal(max, len(pool.repositories))

	arrayID := make([]string, max)
	arrayExpected := make([]string, max)

	for i := 0; i < max; i++ {
		repo, err := pool.GetPos(i)
		require.NoError(err)
		arrayID[i] = repo.ID
		arrayExpected[i] = strconv.Itoa(i)

		iter, err := repo.Repo.CommitObjects()
		require.NoError(err)

		counter := 0
		for {
			commit, err := iter.Next()
			if err == io.EOF {
				break
			}

			require.NoError(err)
			require.NotNil(commit)
			counter++
		}

		require.Equal(9, counter)
	}

	require.ElementsMatch(arrayExpected, arrayID)
}

func TestRepositoryPoolSiva(t *testing.T) {
	require := require.New(t)

	expectedRepos := 3

	pool := NewRepositoryPool()
	path := filepath.Join(
		os.Getenv("GOPATH"),
		"src", "github.com", "src-d", "gitbase",
		"_testdata",
	)

	require.NoError(pool.AddSivaDir(path))
	require.Equal(expectedRepos, len(pool.repositories))

	expected := []int{606, 452, 75}
	result := make([]int, expectedRepos)

	for i := 0; i < expectedRepos; i++ {
		repo, err := pool.GetPos(i)
		require.NoError(err)

		iter, err := repo.Repo.CommitObjects()
		require.NoError(err)

		require.NoError(iter.ForEach(func(c *object.Commit) error {
			result[i]++
			return nil
		}))
	}

	require.Equal(expected, result)
}

var errIter = fmt.Errorf("Error iter")

type newIteratorFunc func(*Repository) (RowRepoIter, error)
type nextFunc func() (sql.Row, error)

type testErrorIter struct {
	newIterator newIteratorFunc
	next        nextFunc
}

func (d *testErrorIter) NewIterator(
	repo *Repository,
) (RowRepoIter, error) {
	if d.newIterator != nil {
		return d.newIterator(repo)
	}

	return nil, errIter
}

func (d *testErrorIter) Next() (sql.Row, error) {
	if d.next != nil {
		return d.next()
	}

	return nil, io.EOF
}

func (d *testErrorIter) Close() error {
	return nil
}

func testCaseRepositoryErrorIter(
	t *testing.T,
	pool *RepositoryPool,
	iter RowRepoIter,
	retError error,
	skipGitErrors bool,
) {
	require := require.New(t)

	timeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ctx := sql.NewContext(timeout,
		sql.WithSession(NewSession(pool, WithSkipGitErrors(skipGitErrors))),
	)

	r, err := NewRowRepoIter(ctx, iter)
	require.NoError(err)

	repoIter, ok := r.(*rowRepoIter)
	require.True(ok)

	go func() {
		for {
			_, err := repoIter.Next()
			if err != nil {
				cancel()
				break
			}
		}
	}()

	<-repoIter.ctx.Done()
}

func TestRepositoryErrorIter(t *testing.T) {
	path := fixtures.Basic().ByTag("worktree").One().Worktree().Root()
	pool := NewRepositoryPool()
	pool.Add(gitRepo("one", path))

	iter := &testErrorIter{}
	testCaseRepositoryErrorIter(t, pool, iter, errIter, false)
}

func TestRepositoryErrorBadRepository(t *testing.T) {
	pool := NewRepositoryPool()
	pool.Add(gitRepo("one", "badpath"))

	iter := &testErrorIter{}

	newIterator := func(*Repository) (RowRepoIter, error) {
		return iter, nil
	}

	count := 0
	next := func() (sql.Row, error) {
		if count >= 10 {
			return nil, io.EOF
		}

		count++

		return sql.NewRow("test " + strconv.Itoa(count)), nil
	}

	iter.newIterator = newIterator
	iter.next = next

	testCaseRepositoryErrorIter(t, pool, iter, git.ErrRepositoryNotExists, false)
	testCaseRepositoryErrorIter(t, pool, iter, io.EOF, true)
}

func TestRepositoryErrorBadRow(t *testing.T) {
	path := fixtures.Basic().ByTag("worktree").One().Worktree().Root()
	pool := NewRepositoryPool()
	pool.Add(gitRepo("one", path))

	iter := &testErrorIter{}

	newIterator := func(*Repository) (RowRepoIter, error) {
		return iter, nil
	}

	errRow := fmt.Errorf("bad row")

	count := 0
	next := func() (sql.Row, error) {
		if count == 5 {
			return nil, errRow
		}

		if count >= 10 {
			return nil, io.EOF
		}

		count++

		return sql.NewRow("test"), nil
	}

	iter.newIterator = newIterator
	iter.next = next

	testCaseRepositoryErrorIter(t, pool, iter, errRow, false)
	testCaseRepositoryErrorIter(t, pool, iter, io.EOF, true)
}

func TestRepositoryIteratorOrder(t *testing.T) {
	path := fixtures.Basic().ByTag("worktree").One().Worktree().Root()
	pool := NewRepositoryPool()
	pool.Add(gitRepo("one", path))

	timeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ctx := sql.NewContext(timeout,
		sql.WithSession(NewSession(pool, WithSkipGitErrors(true))),
	)
	iter := &testErrorIter{}
	newIterator := func(*Repository) (RowRepoIter, error) {
		return iter, nil
	}

	count := 0
	next := func() (sql.Row, error) {
		if count >= 10 {
			return nil, io.EOF
		}

		count++

		return sql.NewRow("test " + strconv.Itoa(count)), nil
	}
	iter.newIterator = newIterator
	iter.next = next

	r, err := NewRowRepoIter(ctx, iter)
	require.NoError(t, err)

	repoIter, ok := r.(*rowRepoIter)
	require.True(t, ok)

	func() {
		for i := 1; i <= 10; i++ {
			row, err := repoIter.Next()
			if err != nil {
				break
			}
			require.Equal(t, sql.Row{"test " + strconv.Itoa(i)}, row)
		}
	}()

	cancel()
}
