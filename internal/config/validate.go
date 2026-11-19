package config

import "fmt"

func Validate(c *Config) error {
	for _, sql := range c.SQL {
		if sql.Database != nil {
			if sql.Database.URI == "" && !sql.Database.Managed {
				return ErrInvalidDatabase
			}
		}
		if sql.SchemaDrift != nil && sql.SchemaDrift.Enabled {
			development := sql.SchemaDrift.Development
			if len(development) == 0 {
				development = sql.Schema
			}
			if len(development) == 0 {
				return fmt.Errorf("schema drift: development schema is required")
			}
			if len(sql.SchemaDrift.Staging) == 0 {
				return fmt.Errorf("schema drift: staging schema is required")
			}
			if len(sql.SchemaDrift.Production) == 0 {
				return fmt.Errorf("schema drift: production schema is required")
			}
		}
	}
	return nil
}
