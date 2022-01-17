package main

/*
Important principles: stateless as much as possible
*/

/*
Target architecture:

Incoming REST call --> http.go
There is one function for that specific call. It parses the parameters and executes further functions:
1. One or multiple function getting the data from the database (database.go)
2. Only one function processing everything. In this function no database calls are allowed to be as stateless as possible (dataprocessing.go)
Then the results are bundled together and a return JSON is created.
*/

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/heptiolabs/healthcheck"
	"github.com/united-manufacturing-hub/united-manufacturing-hub/internal"
	"go.uber.org/zap"
)

var shutdownEnabled bool

var buildtime string

func main() {
	debugEnabled := os.Getenv("DEBUG_ENABLED")

	// Setup logger and set as global
	var logger *zap.Logger
	if debugEnabled == "True" || debugEnabled == "true" {
		logger, _ = zap.NewDevelopment()
	} else {
		logger, _ = zap.NewProduction()
	}

	zap.ReplaceGlobals(logger)
	defer logger.Sync()
	zap.S().Infof("This is factoryinsight build date: %s", buildtime)

	PQHost := "db"
	// Read environment variables
	if os.Getenv("POSTGRES_HOST") != "" {
		PQHost = os.Getenv("POSTGRES_HOST")
	}

	// Read port and convert to integer
	PQPortString := "5432"
	if os.Getenv("POSTGRES_PORT") != "" {
		PQPortString = os.Getenv("POSTGRES_PORT")
	}
	PQPort, err := strconv.Atoi(PQPortString)
	if err != nil {
		zap.S().Errorf("Cannot parse POSTGRES_PORT: not a number", PQPortString)
		return // Abort program
	}

	// Read in other environment variables
	PQUser := os.Getenv("POSTGRES_USER")
	PQPassword := os.Getenv("POSTGRES_PASSWORD")
	PWDBName := os.Getenv("POSTGRES_DATABASE")

	// Loading up user accounts
	accounts := gin.Accounts{}

	zap.S().Debugf("Loading accounts from environment..")

	for i := 1; i <= 100; i++ {
		tempUser := os.Getenv("CUSTOMER_NAME_" + strconv.Itoa(i))
		tempPassword := os.Getenv("CUSTOMER_PASSWORD_" + strconv.Itoa(i))
		if tempUser != "" && tempPassword != "" {
			zap.S().Infof("Added account for " + tempUser)
			accounts[tempUser] = tempPassword
		}
	}

	// also add admin access
	RESTUser := os.Getenv("FACTORYINSIGHT_USER")
	RESTPassword := os.Getenv("FACTORYINSIGHT_PASSWORD")
	accounts[RESTUser] = RESTPassword

	// get currentVersion
	version := os.Getenv("VERSION")

	zap.S().Debugf("Starting program..")

	redisURI := os.Getenv("REDIS_URI")
	redisURI2 := os.Getenv("REDIS_URI2")
	redisURI3 := os.Getenv("REDIS_URI3")
	redisPassword := os.Getenv("REDIS_PASSWORD")
	redisDB := 0 // default database

	internal.InitCache(redisURI, redisURI2, redisURI3, redisPassword, redisDB, debugEnabled)

	zap.S().Debugf("Cache initialized..", redisURI)

	health := healthcheck.NewHandler()
	shutdownEnabled = false
	health.AddLivenessCheck("goroutine-threshold", healthcheck.GoroutineCountCheck(100))
	health.AddReadinessCheck("shutdownEnabled", isShutdownEnabled())
	go http.ListenAndServe("0.0.0.0:8086", health)

	zap.S().Debugf("Healthcheck initialized..", redisURI)

	SetupDB(PQUser, PQPassword, PWDBName, PQHost, PQPort)

	zap.S().Debugf("DB initialized..", PQHost)

	jaegerHost := os.Getenv("JAEGER_HOST")
	jaegerPort := os.Getenv("JAEGER_PORT")

	SetupRestAPI(accounts, version, jaegerHost, jaegerPort)

	zap.S().Debugf("Jaeger & REST API initialized..", jaegerHost, jaegerPort)

	// Allow graceful shutdown
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)

	go func() {
		// before you trapped SIGTERM your process would
		// have exited, so we are now on borrowed time.
		//
		// Kubernetes sends SIGTERM 30 seconds before
		// shutting down the pod.

		sig := <-sigs

		// Log the received signal
		zap.S().Infof("Recieved SIGTERM", sig)

		// ... close TCP connections here.
		ShutdownApplicationGraceful()

	}()

	select {} // block forever
}

func isShutdownEnabled() healthcheck.Check {
	return func() error {
		if shutdownEnabled {
			return fmt.Errorf("shutdown")
		}
		return nil
	}
}

// ShutdownApplicationGraceful shutsdown the entire application including MQTT and database
func ShutdownApplicationGraceful() {
	zap.S().Infof("Shutting down application")
	shutdownEnabled = true

	time.Sleep(4 * time.Minute) // Wait until all remaining open connections are handled

	ShutdownDB()

	zap.S().Infof("Successfull shutdown. Exiting.")

	// Gracefully exit.
	// (Use runtime.GoExit() if you need to call defers)
	os.Exit(0)
}
