package smtpd

import (
	"container/list"
	"expvar"
	"github.com/jhillyerd/inbucket/config"
	"github.com/jhillyerd/inbucket/log"
	"sync"
	"time"
)

var retentionScanCompleted time.Time
var retentionScanCompletedMu sync.RWMutex

var expRetentionDeletesTotal = new(expvar.Int)
var expRetentionPeriod = new(expvar.Int)

// History of certain stats
var retentionDeletesHist = list.New()

// History rendered as comma delim string
var expRetentionDeletesHist = new(expvar.String)

func StartRetentionScanner(ds DataStore) {
	cfg := config.GetDataStoreConfig()
	expRetentionPeriod.Set(int64(cfg.RetentionMinutes * 60))
	if cfg.RetentionMinutes > 0 {
		// Retention scanning enabled
		log.Info("Retention configured for %v minutes", cfg.RetentionMinutes)
		go retentionScanner(ds, time.Duration(cfg.RetentionMinutes) * time.Minute,
			time.Duration(cfg.RetentionSleep) * time.Millisecond)
	} else {
		log.Info("Retention scanner disabled")
	}
}

func retentionScanner(ds DataStore, maxAge time.Duration, sleep time.Duration) {
	start := time.Now()
	for {
		// Prevent scanner from running more than once a minute
		since := time.Since(start)
		if since < time.Minute {
			dur := time.Minute - since
			log.Trace("Retention scanner sleeping for %v", dur)
			time.Sleep(dur)
		}
		start = time.Now()

		// Kickoff scan
		if err := doRetentionScan(ds, maxAge, sleep); err != nil {
			log.Error("Error during retention scan: %v", err)
		}
	}
}

// doRetentionScan does a single pass of all mailboxes looking for messages that can be purged
func doRetentionScan(ds DataStore, maxAge time.Duration, sleep time.Duration) error {
	log.Trace("Starting retention scan")
	cutoff := time.Now().Add(-1 * maxAge)
	mboxes, err := ds.AllMailboxes()
	if err != nil {
		return err
	}

	for _, mb := range mboxes {
		messages, err := mb.GetMessages()
		if err != nil {
			return err
		}
		for _, msg := range messages {
			if msg.Date().Before(cutoff) {
				log.Trace("Purging expired message %v", msg.Id())
				err = msg.Delete()
				if err != nil {
					// Log but don't abort
					log.Error("Failed to purge message %v: %v", msg.Id(), err)
				} else {
					expRetentionDeletesTotal.Add(1)
				}
			}
		}
		// Sleep after completing a mailbox
		time.Sleep(sleep)
	}

	setRetentionScanCompleted(time.Now())

	return nil
}

func setRetentionScanCompleted(t time.Time) {
	retentionScanCompletedMu.Lock()
	defer retentionScanCompletedMu.Unlock()

	retentionScanCompleted = t
}

func getRetentionScanCompleted() time.Time {
	retentionScanCompletedMu.RLock()
	defer retentionScanCompletedMu.RUnlock()

	return retentionScanCompleted
}

func secondsSinceRetentionScanCompleted() interface{} {
	return time.Since(getRetentionScanCompleted()) / time.Second
}

func init() {
	rm := expvar.NewMap("retention")
	rm.Set("SecondsSinceScanCompleted", expvar.Func(secondsSinceRetentionScanCompleted))
	rm.Set("DeletesHist", expRetentionDeletesHist)
	rm.Set("DeletesTotal", expRetentionDeletesTotal)
	rm.Set("Period", expRetentionPeriod)
}
