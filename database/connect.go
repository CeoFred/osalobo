package database

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Define Prometheus metrics
var (
    dbConnections = promauto.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "db_connections",
            Help: "Database connection statistics",
        },
        []string{"state"}, // states: open, idle, max_open, in_use
    )

    dbOperations = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "db_operations_total",
            Help: "Database operations performed",
        },
        []string{"operation"}, // operations: query, error
    )

    dbQueryDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "db_query_duration_seconds",
            Help:    "Database query duration in seconds",
            Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
        },
        []string{"operation"}, // operations: read, write
    )
)

type Config struct {
    Host     string
    Port     string
    Password string
    User     string
    DBName   string
    SSLMode  string
}

var DB *gorm.DB

// Create a custom GORM logger that records metrics
type prometheusLogger struct {
    logger.Interface
}

func (l *prometheusLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
    sql, _ := fc()
    duration := time.Since(begin).Seconds()
    
    // Determine if this is a read or write operation
    operation := "read"
    if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sql)), "INSERT") ||
       strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sql)), "UPDATE") ||
       strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sql)), "DELETE") {
        operation = "write"
    }
    
    // Record query duration
    dbQueryDuration.WithLabelValues(operation).Observe(duration)
    
    // Record operation count
    dbOperations.WithLabelValues("query").Inc()
    
    if err != nil {
        dbOperations.WithLabelValues("error").Inc()
    }
    
    // Call the original logger
    l.Interface.Trace(ctx, begin, fc, err)
}

func Connect(config *Config) {
    var (
        err     error
        port, _ = strconv.ParseUint(config.Port, 10, 32)
        dsn     = fmt.Sprintf(
            "host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
            config.Host, port, config.User, config.Password, config.DBName, config.SSLMode,
        )
    )

    // Create custom logger with Prometheus metrics
    customLogger := &prometheusLogger{
        Interface: logger.Default.LogMode(logger.Silent),
    }

    DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
        NamingStrategy: schema.NamingStrategy{
            SingularTable: false,
        },
        DisableForeignKeyConstraintWhenMigrating: true,
        Logger: customLogger,
    })

    if err != nil {
        dbOperations.WithLabelValues("error").Inc()
        fmt.Println(err.Error())
        panic("failed to connect database")
    }

    fmt.Println("Connection Opened to Database")

    // Start a goroutine to continuously monitor connection stats
    go monitorDBStats()
}

func monitorDBStats() {
    ticker := time.NewTicker(15 * time.Second)
    defer ticker.Stop()

    for range ticker.C {
        if DB != nil {
            sqlDB, err := DB.DB()
            if err != nil {
                continue
            }

            stats := sqlDB.Stats()
            
            // Update connection metrics
            dbConnections.WithLabelValues("open").Set(float64(stats.OpenConnections))
            dbConnections.WithLabelValues("in_use").Set(float64(stats.InUse))
            dbConnections.WithLabelValues("idle").Set(float64(stats.Idle))
            dbConnections.WithLabelValues("max_open").Set(float64(stats.MaxOpenConnections))
        }
    }
}

// Optional: Add helper functions to wrap common database operations with metrics
func WithMetrics(operation string) func(func() error) error {
    return func(f func() error) error {
        start := time.Now()
        err := f()
        duration := time.Since(start).Seconds()
        
        dbQueryDuration.WithLabelValues(operation).Observe(duration)
        dbOperations.WithLabelValues(operation).Inc()
        
        if err != nil {
            dbOperations.WithLabelValues("error").Inc()
        }
        
        return err
    }
}