package auth

import (
	"testing"
	"time"

	"drs/backend/internal/models"
)

func TestPasswordHashing(t *testing.T) {
	password := "SecureAdminSecret123!"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("Failed to hash password: %v", err)
	}

	if !CheckPasswordHash(password, hash) {
		t.Fatalf("Password check failed for valid password")
	}

	if CheckPasswordHash("WrongPassword", hash) {
		t.Fatalf("Password check succeeded for invalid password")
	}
}

func TestJWTGenerationAndValidation(t *testing.T) {
	secret := "test-secret-key-123"
	user := &models.User{
		ID:    "user-uuid-1",
		OrgID: "org-uuid-1",
		Email: "admin@drs.local",
		Role:  models.RoleSuperAdmin,
	}

	token, err := GenerateJWT(user, secret, 1*time.Hour)
	if err != nil {
		t.Fatalf("Failed to generate JWT: %v", err)
	}

	claims, err := ValidateJWT(token, secret)
	if err != nil {
		t.Fatalf("Failed to validate valid JWT: %v", err)
	}

	if claims.UserID != user.ID || claims.Email != user.Email || claims.Role != models.RoleSuperAdmin {
		t.Fatalf("Claims mismatch: got %+v, expected %+v", claims, user)
	}

	// Test invalid secret
	_, err = ValidateJWT(token, "wrong-secret")
	if err == nil {
		t.Fatalf("Expected validation error with wrong secret, got nil")
	}

	// Test expired token
	expiredToken, err := GenerateJWT(user, secret, -1*time.Hour)
	if err != nil {
		t.Fatalf("Failed to generate expired JWT: %v", err)
	}

	_, err = ValidateJWT(expiredToken, secret)
	if err == nil {
		t.Fatalf("Expected validation error for expired token, got nil")
	}
}
