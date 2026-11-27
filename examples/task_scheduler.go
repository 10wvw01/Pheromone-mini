package example

import "triggerEngine/api"

// registerApps registers every App before the SDK starts sending notifications.
func registerApps(configPaths ...string) error {
	for _, path := range configPaths {
		if err := api.TriggerInit(path); err != nil {
			return err
		}
	}
	return nil
}
