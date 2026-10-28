package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mazhit-a/mini-kedo/internal/domain"
)

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

const userColumns = `id::text, email, password_hash, full_name, created_at`

func (r *UserRepository) Create(ctx context.Context, u domain.User) (domain.User, error) {
	var out domain.User
	err := Conn(ctx, r.pool).QueryRow(ctx,
		`INSERT INTO users (id, email, password_hash, full_name, created_at)
		 VALUES ($1::uuid, $2, $3, $4, $5)
		 RETURNING `+userColumns,
		u.ID, strings.ToLower(u.Email), u.PasswordHash, u.FullName, u.CreatedAt,
	).Scan(&out.ID, &out.Email, &out.PasswordHash, &out.FullName, &out.CreatedAt)
	if err != nil {
		if isUniqueViolation(err, "users_email_key") {
			return domain.User{}, domain.ErrEmailTaken
		}
		return domain.User{}, fmt.Errorf("insert user: %w", err)
	}
	return out, nil
}

func (r *UserRepository) GetByID(ctx context.Context, id string) (domain.User, error) {
	if !validUUID(id) {
		return domain.User{}, domain.ErrNotFound
	}
	var out domain.User
	err := Conn(ctx, r.pool).QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1::uuid`, id).
		Scan(&out.ID, &out.Email, &out.PasswordHash, &out.FullName, &out.CreatedAt)
	if err != nil {
		return domain.User{}, mapNoRows(err)
	}
	return out, nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (domain.User, error) {
	var out domain.User
	err := Conn(ctx, r.pool).QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, strings.ToLower(email)).
		Scan(&out.ID, &out.Email, &out.PasswordHash, &out.FullName, &out.CreatedAt)
	if err != nil {
		return domain.User{}, mapNoRows(err)
	}
	return out, nil
}
