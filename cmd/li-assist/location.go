package main

import "fmt"

// resolveLocation applies the location-resolution precedence for both
// 'jobs search' and 'jobs sweep':
//
//  1. if anywhere is true   → "" (worldwide; error if locationFlag is also set)
//  2. else if locationFlag  → locationFlag value
//  3. else                  → homeLocation from config (may be "")
func resolveLocation(anywhere bool, locationFlag, homeLocation string) (string, error) {
	if anywhere && locationFlag != "" {
		return "", fmt.Errorf("--anywhere and --location are mutually exclusive: use one or the other")
	}
	if anywhere {
		return "", nil
	}
	if locationFlag != "" {
		return locationFlag, nil
	}
	return homeLocation, nil
}
