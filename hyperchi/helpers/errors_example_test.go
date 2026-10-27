package helpers_test

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/regiellis/hyperchi/hyperchi/helpers"
)

func ExampleMust() {
	// A nil error passes through. A non-nil one would log and panic.
	helpers.Must(nil)
	fmt.Println("Success: no error to handle")

	// Output: Success: no error to handle
}

func ExampleMustValue() {
	// MustValue takes the (value, error) pair directly. A parse failure
	// would panic.
	result := helpers.MustValue(strconv.Atoi("42"))
	fmt.Printf("Parsed number: %d\n", result)

	// Output: Parsed number: 42
}

func ExampleTry() {
	err := helpers.Try(func() error {
		return nil
	})

	if err != nil {
		fmt.Printf("Operation failed: %v\n", err)
	} else {
		fmt.Println("Operation succeeded")
	}

	// Output: Operation succeeded
}

func ExampleCatch() {
	helpers.Catch(func() error {
		return errors.New("operation failed")
	}, func(err error) {
		fmt.Printf("Caught error: %v\n", err)
	})

	// Output: Caught error: operation failed
}

func ExampleCatchValue() {
	result := helpers.CatchValue(func() (string, error) {
		return "", errors.New("failed to get value")
	}, func(err error) string {
		fmt.Printf("Using fallback due to: %v\n", err)
		return "default-value"
	})

	fmt.Printf("Final result: %s\n", result)

	// Output: Using fallback due to: failed to get value
	// Final result: default-value
}

func ExampleDefault() {
	value1 := helpers.Default("success", nil, "fallback")
	fmt.Printf("Result 1: %s\n", value1)

	value2 := helpers.Default("", errors.New("failed"), "fallback")
	fmt.Printf("Result 2: %s\n", value2)

	// Output: Result 1: success
	// Result 2: fallback
}

func ExampleIgnoreValue() {
	// A failed lookup leaves hostname empty.
	hostname := helpers.IgnoreValue(os.Hostname())
	if hostname != "" {
		fmt.Printf("Running on: %s\n", hostname)
	} else {
		fmt.Println("Hostname not available")
	}

	// A parse failure would give 0.
	port := helpers.IgnoreValue(strconv.Atoi("8080"))
	fmt.Printf("Port: %d\n", port)

	// No output check: the hostname differs per machine.
}

func ExampleNewChain() {
	chain := helpers.NewChain().
		Do(func() error {
			fmt.Println("Step 1: Initializing...")
			return nil
		}).
		Do(func() error {
			fmt.Println("Step 2: Configuring...")
			return nil
		}).
		Do(func() error {
			fmt.Println("Step 3: Starting...")
			return nil
		})

	if chain.Success() {
		fmt.Println("All steps completed successfully")
	} else {
		fmt.Printf("Chain failed: %v\n", chain.Error())
	}

	// Output: Step 1: Initializing...
	// Step 2: Configuring...
	// Step 3: Starting...
	// All steps completed successfully
}

// A failed step stops the chain: step 3 never runs.
func ExampleChain_withFailure() {
	chain := helpers.NewChain().
		Do(func() error {
			fmt.Println("Step 1: Success")
			return nil
		}).
		Do(func() error {
			fmt.Println("Step 2: Failure")
			return errors.New("step 2 failed")
		}).
		Do(func() error {
			fmt.Println("Step 3: This won't execute")
			return nil
		})

	if !chain.Success() {
		fmt.Printf("Chain failed at: %v\n", chain.Error())
	}

	// Output: Step 1: Success
	// Step 2: Failure
	// Chain failed at: step 2 failed
}

func ExampleGetErrorInfo() {
	err := errors.New("sample error")
	errorInfo := helpers.GetErrorInfo(err)

	if errorInfo != nil {
		fmt.Printf("Error occurred in function: %s\n", errorInfo.Function)
		fmt.Printf("Error message: %v\n", errorInfo.Err)
	}

	// No output check: File, Line, and Function depend on the build.
}

func ExampleErrorHandler() {
	handler := helpers.NewErrorHandler().WithLogger(func(format string, args ...any) {
		fmt.Printf("[CUSTOM] "+format+"\n", args...)
	})

	// A non-nil error would be logged through the custom logger.
	handler.Must(nil)
	fmt.Println("Custom handler ready")

	// A silent handler would panic on an error without logging it.
	silentHandler := helpers.NewErrorHandler().Silent()
	silentHandler.Must(nil)
	fmt.Println("Silent handler ready")

	// Output: Custom handler ready
	// Silent handler ready
}

func ExampleSetDefaultLogger() {
	helpers.SetDefaultLogger(func(format string, args ...any) {
		fmt.Printf("[PKG] "+format+"\n", args...)
	})

	// Restore the standard logger so other examples are unaffected.
	helpers.SetDefaultLogger(log.Printf)
	fmt.Println("Default logger configured")

	// Output: Default logger configured
}

// Example_practicalUsage combines a config fallback, a setup chain, and a
// default value.
func Example_practicalUsage() {
	config := helpers.CatchValue(func() (string, error) {
		return "production-config", nil
	}, func(err error) string {
		return "default-config"
	})

	fmt.Printf("Using config: %s\n", config)

	// Each step runs only if the previous one succeeded.
	chain := helpers.NewChain().
		Do(func() error {
			fmt.Println("Connecting to database...")
			return nil
		}).
		Do(func() error {
			fmt.Println("Running migrations...")
			return nil
		}).
		Do(func() error {
			fmt.Println("Seeding data...")
			return nil
		})

	if chain.Success() {
		fmt.Println("Database setup complete")
	}

	envVersion := os.Getenv("APP_VERSION")
	version := helpers.Default(envVersion, nil, "unknown")
	if envVersion == "" {
		version = "unknown"
	}
	fmt.Printf("App version: %s\n", version)

	// Output: Using config: production-config
	// Connecting to database...
	// Running migrations...
	// Seeding data...
	// Database setup complete
	// App version: unknown
}
