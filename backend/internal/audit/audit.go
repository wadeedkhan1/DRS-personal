package audit

import (
	"database/sql"
	"encoding/json"
	"log"

	"drs/backend/internal/models"
)

type Logger struct {
	db      *sql.DB
	logChan chan *models.AuditLog
}

func NewLogger(db *sql.DB) *Logger {
	l := &Logger{
		db:      db,
		logChan: make(chan *models.AuditLog, 256),
	}
	go l.worker()
	return l
}

func (l *Logger) Log(orgID, actorUserID, actorEmail, action, targetType, targetID string, metadata map[string]interface{}, ip string) {
	var metaJSON json.RawMessage
	if metadata != nil {
		bytes, err := json.Marshal(metadata)
		if err == nil {
			metaJSON = bytes
		}
	}

	var orgIDPtr *string
	if orgID != "" {
		orgIDPtr = &orgID
	}

	var actorIDPtr *string
	if actorUserID != "" {
		actorIDPtr = &actorUserID
	}

	entry := &models.AuditLog{
		OrgID:       orgIDPtr,
		ActorUserID: actorIDPtr,
		ActorEmail:  actorEmail,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		Metadata:    metaJSON,
		IPAddress:   ip,
	}

	select {
	case l.logChan <- entry:
	default:
		log.Printf("[AUDIT WARN] Log channel full, dropping log entry for action: %s", action)
	}
}

func (l *Logger) worker() {
	query := `
		INSERT INTO audit_logs (org_id, actor_user_id, actor_email, action, target_type, target_id, metadata, ip_address)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	for entry := range l.logChan {
		meta := entry.Metadata
		if len(meta) == 0 {
			meta = json.RawMessage("{}")
		}

		_, err := l.db.Exec(query,
			entry.OrgID,
			entry.ActorUserID,
			entry.ActorEmail,
			entry.Action,
			entry.TargetType,
			entry.TargetID,
			meta,
			entry.IPAddress,
		)
		if err != nil {
			log.Printf("[AUDIT ERROR] Failed to write audit log: %v", err)
		}
	}
}
