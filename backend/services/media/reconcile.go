package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

)

// reconcileWorker periodically re-reads protected objects from both independent
// origins. A crash leaves a short lease, not a 24-hour blind spot.
func (s *server) reconcileWorker(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		if err := s.reconcileProtectedBatch(ctx, 25); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("media replica reconciliation: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type reconcileItem struct {
	owner, namespace, key, contentType, expectedSHA string
	size int64
}

func (s *server) reconcileProtectedBatch(parent context.Context, limit int) error {
	if limit < 1 || limit > 100 {
		return errors.New("reconciliation batch limit must be between 1 and 100")
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `select owner_id,namespace,object_key,content_type,size_bytes,coalesce(sha256,'')
		from media_objects
		where status='protected'
		  and coalesce(reconciled_at,replica_verified_at,updated_at) <= now()-interval '24 hours'
		  and (reconciliation_claimed_at is null or reconciliation_claimed_at < now()-interval '15 minutes')
		order by coalesce(reconciled_at,replica_verified_at,updated_at),owner_id,object_key
		limit $1 for update skip locked`, limit)
	if err != nil {
		return err
	}
	var items []reconcileItem
	for rows.Next() {
		var item reconcileItem
		if err := rows.Scan(&item.owner, &item.namespace, &item.key, &item.contentType, &item.size, &item.expectedSHA); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range items {
		if _, err := tx.Exec(ctx, `update media_objects set reconciliation_claimed_at=now()
			where owner_id=$1 and namespace=$2 and object_key=$3 and status='protected'`,
			item.owner, item.namespace, item.key); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	for _, item := range items {
		primaryOK := s.originMatches(ctx, s.cfg.primary, item)
		replicaOK := s.originMatches(ctx, s.cfg.replica, item)
		switch {
		case primaryOK && replicaOK:
			_, err = s.db.Exec(ctx, `update media_objects set reconciled_at=now(),reconciliation_claimed_at=null
				where owner_id=$1 and namespace=$2 and object_key=$3 and status='protected'`,
				item.owner, item.namespace, item.key)
		case primaryOK && !replicaOK:
			err = s.requeueReplica(ctx, item)
		default:
			// Fail closed: do not serve an object whose primary origin no longer
			// matches its verified digest. If the replica is intact, operators
			// can restore the primary without exposing an unverified object.
			_, err = s.db.Exec(ctx, `update media_objects set status='replication_failed',
				reconciled_at=now(),reconciliation_claimed_at=null,updated_at=now()
				where owner_id=$1 and namespace=$2 and object_key=$3 and status='protected'`,
				item.owner, item.namespace, item.key)
			if err == nil {
				log.Printf("media reconciliation quarantined object owner=%s namespace=%s key=%s primary_ok=false replica_ok=%t",
					item.owner, item.namespace, item.key, replicaOK)
			}
		}
		if err != nil {
			// Expire the claim quickly so transient DB failures are retried.
			_, _ = s.db.Exec(ctx, `update media_objects set reconciliation_claimed_at=null
				where owner_id=$1 and namespace=$2 and object_key=$3 and status='protected'`,
				item.owner, item.namespace, item.key)
			return err
		}
	}
	return nil
}

func (s *server) originMatches(ctx context.Context, store s3Store, item reconcileItem) bool {
	resp, err := s.signedRequest(ctx, store, http.MethodGet, item.key, nil, "")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || (resp.ContentLength >= 0 && resp.ContentLength != item.size) {
		return false
	}
	if got := resp.Header.Get("Content-Type"); got != "" && got != item.contentType {
		return false
	}
	return digestMatches(resp.Body, item.size, item.expectedSHA)
}

func (s *server) requeueReplica(ctx context.Context, item reconcileItem) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `update media_objects set status='replication_pending',replica_verified_at=null,
		reconciled_at=now(),reconciliation_claimed_at=null,updated_at=now()
		where owner_id=$1 and namespace=$2 and object_key=$3 and status='protected'`,
		item.owner, item.namespace, item.key)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `insert into media_outbox(owner_id,namespace,object_key,operation,status,attempts,available_at,locked_until,last_error)
		values($1,$2,$3,'replicate','pending',0,now(),null,'replica failed periodic reconciliation')
		on conflict(owner_id,namespace,object_key,operation) do update
		set status='pending',attempts=0,available_at=now(),locked_until=null,last_error='replica failed periodic reconciliation',updated_at=now()`,
		item.owner, item.namespace, item.key)
	if err == nil {
		eventID, idErr := randomID()
		if idErr != nil {
			err = idErr
		} else {
			_, err = tx.Exec(ctx, `insert into media_operations(operation_id,owner_id,namespace,object_key,operation,outcome,detail)
				values($1,$2,$3,$4,'replicate','queued',$5)`,
				eventID, item.owner, item.namespace, item.key, "scheduled after periodic replica verification")
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return err
}

func digestMatches(body io.Reader, size int64, expected string) bool {
	if body == nil || size < 0 || len(expected) != 64 {
		return false
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(body, size+1))
	return err == nil && n == size && hex.EncodeToString(hash.Sum(nil)) == expected
}
