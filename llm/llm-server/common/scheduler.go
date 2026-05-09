package common

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/config"
	"time"

	"github.com/go-co-op/gocron/v2"
)

var localScheduler gocron.Scheduler
var leaderScheduler gocron.Scheduler

var schedulerInitialized = false

func init() {
	error := startScheduler()
	if error != nil {
		slog.Error("scheduler: failed to start scheduler", "error", error)
		panic(error)
	}
	schedulerInitialized = true
	err := registerInitJobs()
	if err != nil {
		slog.Error("scheduler: failed to register init jobs", "error", err)
		panic(err)
	}
}

func startScheduler() error {
	localScheduler1, err := gocron.NewScheduler(gocron.WithLocation(time.UTC))
	if err != nil {
		slog.Error("scheduler: failed to create scheduler", "error", err)
		return err
	}
	localScheduler = localScheduler1

	leaderScheduler1, err := gocron.NewScheduler(gocron.WithLocation(time.UTC), gocron.WithDistributedElector(&dbElector{}))
	if err != nil {
		slog.Error("scheduler: failed to create scheduler", "error", err)
		return err
	}
	leaderScheduler = leaderScheduler1

	localScheduler.Start()
	leaderScheduler.Start()
	return nil
}

func registerInitJobs() error {
	err := NewLocalIntervalJob("worker_heartbeat", registerOrUpdateWorker, time.Duration(config.Config.ServerHeartBeatFrequncySecond)*time.Second)
	if err != nil {
		slog.Error("scheduler: failed to create worker heartbeat job", "error", err)
		return err
	}

	err = NewLeaderIntervalJob("worker_sync", workerSync, time.Duration(config.Config.ServerHeartBeatTimeoutSecond)*time.Second)
	if err != nil {
		slog.Error("scheduler: failed to create worker heartbeat job", "error", err)
		return err
	}

	return nil
}

func NewLocalIntervalJob(jobName string, job func() error, interval time.Duration) error {
	if !schedulerInitialized {
		slog.Error("scheduler: scheduler not initialized")
		return fmt.Errorf("scheduler: not initialized")
	}
	_, err := localScheduler.NewJob(gocron.DurationJob(interval), gocron.NewTask(job), gocron.WithName(jobName), gocron.WithSingletonMode(gocron.LimitModeReschedule))
	if err != nil {
		slog.Error("scheduler: failed to create worker heartbeat job", "error", err)
		return err
	}
	return nil
}

func NewPooledJob(jobName string, job func() error) error {
	if !schedulerInitialized {
		slog.Error("scheduler: scheduler not initialized")
		return fmt.Errorf("scheduler: not initialized")
	}
	_, err := localScheduler.NewJob(gocron.OneTimeJob(gocron.OneTimeJobStartImmediately()), gocron.NewTask(job), gocron.WithName(jobName))
	if err != nil {
		slog.Error("scheduler: failed to create worker heartbeat job", "error", err)
		return err
	}
	return nil
}

func NewLeaderIntervalJob(jobName string, job func() error, interval time.Duration) error {
	if !schedulerInitialized {
		slog.Error("scheduler: scheduler not initialized")
		return fmt.Errorf("scheduler: not initialized")
	}
	_, err := leaderScheduler.NewJob(gocron.DurationJob(interval), gocron.NewTask(job), gocron.WithName(jobName), gocron.WithSingletonMode(gocron.LimitModeReschedule))
	if err != nil {
		slog.Error("scheduler: failed to create worker heartbeat job", "error", err)
		return err
	}
	return nil
}

func workerSync() error {
	dbms, err := GetDatabaseManager(Metastore)
	if err != nil {
		slog.Error("scheduler: failed to get database manager", "error", err)
		return err
	}
	rows, err := dbms.Db.Queryx("select worker_name, updated_at from nb_workers where worker_type=$1 and updated_at < timezone('utc',now()) - ($2)::interval", config.SERVICE_NAME, fmt.Sprintf("%d second", config.Config.ServerHeartBeatTimeoutSecond))
	if err != nil {
		slog.Error("scheduler: failed to query leader status", "error", err)
		return err
	}

	type workerToDelete struct {
		Name      string
		UpdatedAt time.Time
	}
	var workers []workerToDelete

	for rows.Next() {
		var w workerToDelete
		if err := rows.Scan(&w.Name, &w.UpdatedAt); err != nil {
			slog.Error("scheduler: failed to scan worker", "error", err)
			continue
		}
		workers = append(workers, w)
	}
	if err := rows.Err(); err != nil {
		slog.Error("scheduler: failed to iterate rows", "error", err)
		return err
	}
	if err := rows.Close(); err != nil {
		slog.Error("scheduler: failed to close rows", "error", err)
	}

	for _, w := range workers {
		slog.Info("scheduler: worker is not responding, remvoing worker from workers", "worker_name", w.Name, "updated_at", w.UpdatedAt)
		_, err = dbms.Db.Exec(`delete from nb_workers where worker_type=$1 and worker_name=$2`, config.SERVICE_NAME, w.Name)
		if err != nil {
			slog.Error("scheduler: failed to delete worker", "error", err, "worker_name", w.Name)
		}
	}

	return nil
}

func registerOrUpdateWorker() error {
	dbms, err := GetDatabaseManager(Metastore)
	if err != nil {
		slog.Error("scheduler: failed to get database manager", "error", err)
		return err
	}
	_, err = dbms.Db.Exec(`INSERT INTO nb_workers (worker_type, worker_name, updated_at) 
		VALUES ($1, $2, timezone('utc',now())) 
		ON CONFLICT (worker_type, worker_name) DO UPDATE SET updated_at = EXCLUDED.updated_at`,
		config.SERVICE_NAME, config.Config.ServerName)
	if err != nil {
		slog.Error("scheduler: failed to register/update worker", "error", err)
		return err
	}

	updatedRows, err := dbms.Db.Exec(`update nb_workers 
			set is_leader=true, updated_at = timezone('utc',now())
			where worker_type=$1 and worker_name=$2 
				and not exists(select * from nb_workers where worker_type=$1 and is_leader = true and updated_at > timezone('utc',now()) - ($3)::interval)
		`, config.SERVICE_NAME, config.Config.ServerName, fmt.Sprintf("%d second", config.Config.ServerHeartBeatTimeoutSecond))
	if err != nil {
		slog.Error("scheduler: failed to register/update worker", "error", err)
		return err
	}

	affectedRows, err := updatedRows.RowsAffected()
	if err != nil {
		slog.Error("scheduler: failed to get affected rows", "error", err)
		return err
	}

	if affectedRows > 0 {
		slog.Info("scheduler: registered as leader, marking others to workers", "service_name", config.SERVICE_NAME, "server_name", config.Config.ServerName)
		_, err := dbms.Db.Exec(`update nb_workers 
		set is_leader=false, updated_at = timezone('utc',now())
		where worker_type=$1 and worker_name!=$2 
	`, config.SERVICE_NAME, config.Config.ServerName)
		if err != nil {
			slog.Error("scheduler: failed to register/update worker", "error", err)
			return err
		}
	}

	return nil
}

func StopScheduler() {
	if localScheduler != nil {
		err := localScheduler.Shutdown()
		if err != nil {
			slog.Error("scheduler: failed to stop local-scheduler", "error", err)
		}
	}
	if leaderScheduler != nil {
		err := leaderScheduler.Shutdown()
		if err != nil {
			slog.Error("scheduler: failed to stop leader-scheduler", "error", err)
		}
	}
}

type dbElector struct {
}

func (e *dbElector) IsLeader(context.Context) error {
	dbms, err := GetDatabaseManager(Metastore)
	if err != nil {
		slog.Error("scheduler: failed to get database manager", "error", err)
		return err
	}
	row := dbms.Db.QueryRowx("select count(*) from nb_workers where worker_type=$1 and worker_name=$2 and is_leader=true", config.SERVICE_NAME, config.Config.ServerName)
	if row.Err() != nil {
		slog.Error("scheduler: failed to query leader status", "error", row.Err())
		return row.Err()
	}
	var count int
	err = row.Scan(&count)
	if err != nil {
		slog.Error("scheduler: failed to scan leader status", "error", err)
		return err
	}
	if count == 0 {
		return errors.New("scheduler: not a leader")
	}

	return nil
}
