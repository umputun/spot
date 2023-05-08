package secrets

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	_ "github.com/go-sql-driver/mysql" // mysql driver loaded here
	_ "github.com/lib/pq"              // postgres driver loaded here
	_ "modernc.org/sqlite"             // sqlite driver loaded here

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/nacl/secretbox"
)

// InternalProvider is a secret provider that stores secrets in a database, encrypted.
// supported database types: sqlite, postgres, mysql
type InternalProvider struct {
	db     *sql.DB
	key    []byte
	dbType string // add dbType field to struct
}

// NewInternalProvider creates a new InternalProvider.
func NewInternalProvider(conn string, key []byte) (*InternalProvider, error) {
	dbType := func(c string) (string, error) {
		if strings.HasPrefix(c, "postgres://") {
			return "postgres", nil
		}
		if strings.Contains(c, "@tcp(") {
			return "mysql", nil
		}
		if strings.HasPrefix(c, "file:/") || strings.HasSuffix(c, ".sqlite") || strings.HasSuffix(c, ".db") {
			return "sqlite", nil
		}
		return "", fmt.Errorf("unsupported database type in connection string")
	}

	dbt, err := dbType(conn)
	if err != nil {
		return nil, fmt.Errorf("can't determine database type: %w", err)
	}

	db, err := sql.Open(dbt, conn)
	if err != nil {
		return nil, fmt.Errorf("error opening secrets database: %w", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS spot_secrets (skey VARCHAR(255) PRIMARY KEY, sval TEXT);`)
	if err != nil {
		return nil, err
	}
	log.Printf("[INFO] secrets provider: using %s database, type: %s", conn, dbt)
	return &InternalProvider{db: db, dbType: dbt, key: key}, nil
}

// Get retrieves a secret from the database, decrypts it, and returns it.
func (p *InternalProvider) Get(key string) (string, error) {
	var encryptedData []byte

	loadStmt := "SELECT sval FROM spot_secrets WHERE skey = ?"
	if p.dbType == "postgres" {
		loadStmt = "SELECT sval FROM spot_secrets WHERE skey = $1"
	}
	stmt, err := p.db.Prepare(loadStmt)
	if err != nil {
		return "", err
	}
	defer stmt.Close()

	if err = stmt.QueryRow(key).Scan(&encryptedData); err != nil {
		if err == sql.ErrNoRows {
			return "", errors.New("secret not found")
		}
		return "", err
	}

	decrypted, err := p.decrypt(string(encryptedData))
	if err != nil {
		return "", fmt.Errorf("can't get secret for %s: %w", key, err)
	}

	return decrypted, nil
}

// Set stores a secret in the database, encrypted.
func (p *InternalProvider) Set(key, value string) error {
	encryptedData, err := p.encrypt(value)
	if err != nil {
		return fmt.Errorf("can't set secret for %s: %w", key, err)
	}

	// use database-specific "INSERT" statements
	var insertStmt string
	switch p.dbType {
	case "sqlite":
		insertStmt = "INSERT OR REPLACE INTO spot_secrets (skey, sval) VALUES ($1, $2)"
	case "postgres":
		insertStmt = "INSERT INTO spot_secrets (skey, sval) VALUES ($1, $2) ON CONFLICT (skey) DO UPDATE SET sval = $2;"
	case "mysql":
		insertStmt = "REPLACE INTO spot_secrets (skey, sval) VALUES (?, ?)"
	default:
		return fmt.Errorf("unsupported database type: %s", p.dbType)
	}

	stmt, err := p.db.Prepare(insertStmt)
	if err != nil {
		return fmt.Errorf("error preparing insert statement: %w", err)
	}
	defer stmt.Close()

	if _, err = stmt.Exec(key, encryptedData); err != nil {
		return fmt.Errorf("error inserting secret: %w", err)
	}
	return nil
}

// Delete removes a secret from the database.
func (p *InternalProvider) Delete(key string) error {
	deleteStmt := "DELETE FROM spot_secrets WHERE skey = ?"
	if p.dbType == "postgres" {
		deleteStmt = "DELETE FROM spot_secrets WHERE skey = $1"
	}
	stmt, err := p.db.Prepare(deleteStmt)
	if err != nil {
		return fmt.Errorf("error preparing delete statement: %w", err)
	}
	defer stmt.Close()

	res, err := stmt.Exec(key)
	if err != nil {
		return fmt.Errorf("error deleting secret for %s: %w", key, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("error checking affected rows: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("key not found in the database: %s", key)
	}

	return nil
}

// List retrieves a list of secret keys from the database with an optional prefix filter.
func (p *InternalProvider) List(prefix string) ([]string, error) {
	var keys []string
	var rows *sql.Rows
	var err error

	listStmt := "SELECT skey FROM spot_secrets"
	if prefix != "*" && prefix != "" {
		if p.dbType == "postgres" {
			listStmt = "SELECT skey FROM spot_secrets WHERE skey LIKE $1"
		} else {
			listStmt = "SELECT skey FROM spot_secrets WHERE skey LIKE ?"
		}
		rows, err = p.db.Query(listStmt, prefix+"%")
	} else {
		rows, err = p.db.Query(listStmt)
	}
	if err != nil {
		return nil, fmt.Errorf("error listing secrets: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("error scanning secret keys: %w", err)
		}
		keys = append(keys, key)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error retrieving secret keys: %w", err)
	}

	return keys, nil
}

// encrypt is a helper function that takes a plaintext data string as input and
// returns an encrypted version of the data using the NaCl Secretbox encryption
// scheme. The encryption process consists of the following steps:
// 1. Generate a random 16-byte salt.
// 2. Derive a 32-byte encryption key from the InternalProvider's key and the salt using the deriveKey function.
// 3. Generate a random 24-byte nonce.
// 4. Encrypt the plaintext data using the derived key, nonce, and NaCl Secretbox.
// 5. Prepend the nonce and salt to the encrypted data.
// 6. Encode the resulting byte slice in base64 format.
func (p *InternalProvider) encrypt(data string) (string, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}

	keyBytes := deriveKey(p.key, salt)
	naclKey := new([32]byte)
	copy(naclKey[:], keyBytes)

	nonce := new([24]byte)
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return "", err
	}

	out := make([]byte, 24+16)
	copy(out, nonce[:])
	copy(out[24:], salt)

	sealed := secretbox.Seal(out, []byte(data), nonce, naclKey)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decrypt is a helper function that takes an encrypted data string in base64 format as input
// and returns the decrypted plaintext version of the data. The decryption process consists of
// the following steps:
// 1. Decode the base64-encoded input data.
// 2. Extract the 24-byte nonce and 16-byte salt from the decoded data.
// 3. Derive a 32-byte decryption key from the InternalProvider's key and the salt using the deriveKey function.
// 4. Decrypt the remaining encrypted data using the derived key, nonce, and NaCl Secretbox.
// 5. Return the decrypted data as a string.
func (p *InternalProvider) decrypt(encodedData string) (string, error) {
	sealed, err := base64.StdEncoding.DecodeString(encodedData)
	if err != nil {
		return "", err
	}

	nonce := new([24]byte)
	copy(nonce[:], sealed[:24])

	salt := sealed[24:40]
	keyBytes := deriveKey(p.key, salt)
	naclKey := new([32]byte)
	copy(naclKey[:], keyBytes)

	decrypted, ok := secretbox.Open(nil, sealed[40:], nonce, naclKey)
	if !ok {
		return "", errors.New("failed to decrypt")
	}
	return string(decrypted), nil
}

// deriveKey is a helper function that takes a user-provided key and a salt as input
// and generates a derived key using the Argon2id key derivation function.
// Argon2id is recommended for password hashing and provides a good balance between
// security and performance. The function uses the following parameters:
// - Time cost: 2 iterations, which increases the time required for key derivation.
// - Memory cost: 64 MiB, which increases the memory required for key derivation.
// - Parallelism: 4, which adjusts the number of threads used for key derivation.
// - Key length: 32 bytes, which is the length of the derived key.
//
// The derived key is then used for encryption and decryption of secrets stored in the database.
func deriveKey(key, salt []byte) []byte {
	return argon2.IDKey(key, salt, 1, 64*1024, 4, 32)
}

// NoOpProvider is a provider that does nothing.
type NoOpProvider struct{}

// Get returns an error on every key.
func (p *NoOpProvider) Get(_ string) (string, error) {
	return "", errors.New("not implemented")
}
