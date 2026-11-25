package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordHash(t *testing.T) {
	hash, err := hashPassword("  password-1  ")
	require.NoError(t, err)
	assert.NotContains(t, hash, "password-1")

	cost, err := bcrypt.Cost([]byte(hash))
	require.NoError(t, err)
	assert.Equal(t, bcryptCost, cost)

	ok, err := comparePassword(hash, "password-1")
	require.NoError(t, err)
	assert.True(t, ok, "пробелы по краям не считаются")

	ok, err = comparePassword(hash, "password-2")
	require.NoError(t, err)
	assert.False(t, ok)

	// пустой или испорченный хеш — «не подошёл», а не сбой
	ok, err = comparePassword("", "password-1")
	require.NoError(t, err)
	assert.False(t, ok)

	other, err := hashPassword("password-1")
	require.NoError(t, err)
	assert.NotEqual(t, hash, other, "у каждого хеша своя соль")
}
