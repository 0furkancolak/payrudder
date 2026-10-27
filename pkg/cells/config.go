// Package cells is the operator's side of cells mode (ADR 0018): moving a tenant between the pooled database and cells, and checking that the directory and the
// databases agree (the doctor). The services only ever read the directory (pkg/tenantdir) and route by it (pkg/pgxx); everything here runs from payrudderctl, inside
// the network, with the owner role of each database.
package cells

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
)

// Config says where everything is. It is a JSON file; "${NAME}" in a string is replaced by the environment variable NAME, so DSNs with passwords stay out of it.
//
//	{
//	  "plans_addr": "plans-svc:7001",
//	  "cells": {"c1": {"region": "eu"}, "c2": {"region": "us"}},
//	  "services": {
//	    "ingest": {
//	      "migrations": "services/ingest-svc/migrations/tenant",
//	      "roles": ["ingest_app", "ingest_system"],
//	      "pooled": "${INGEST_MIGRATOR_DSN}",
//	      "cells": {"c1": "${INGEST_C1_MIGRATOR_DSN}", "c2": "${INGEST_C2_MIGRATOR_DSN}"}
//	    }
//	  }
//	}
type Config struct {
	// PlansAddr is plans-svc's gRPC address (DirectoryService), reached from inside the network.
	PlansAddr string `json:"plans_addr"`
	// Cells are the cells and what the directory records about them.
	Cells map[string]Cell `json:"cells"`
	// Services are the services that have a tenant schema, by a short name.
	Services map[string]Service `json:"services"`
}

// Cell is what the directory records of a cell.
type Cell struct {
	Region string `json:"region"`
}

// Service is one service's databases.
type Service struct {
	// Migrations is the directory of the service's tenant migrations (services/<svc>/migrations/tenant).
	Migrations string `json:"migrations"`
	// Roles are the roles that use the tenant schemas (the app and system roles of the service).
	Roles []string `json:"roles"`
	// Pooled is the migrator DSN of the service's pooled database.
	Pooled string `json:"pooled"`
	// Cells are the migrator DSNs of the service's database in each cell.
	Cells map[string]string `json:"cells"`
}

var name = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// Load reads and checks a config file.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the operator's own file
	if err != nil {
		return Config{}, err
	}
	return Parse(raw)
}

// Parse reads and checks a config.
func Parse(raw []byte) (Config, error) {
	var c Config
	dec := json.NewDecoder(stringsReader(os.ExpandEnv(string(raw))))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("cells: config: %w", err)
	}
	return c, c.Validate()
}

// Validate checks the config is complete: every service has its migrations, roles and a DSN for the pooled database and for every cell.
func (c Config) Validate() error {
	if len(c.Services) == 0 {
		return fmt.Errorf("cells: config has no services")
	}
	for n, cell := range c.Cells {
		if !name.MatchString(n) {
			return fmt.Errorf("cells: cell name %q must match %s", n, name)
		}
		if cell.Region != "" && !name.MatchString(cell.Region) {
			return fmt.Errorf("cells: region %q of cell %s must match %s", cell.Region, n, name)
		}
	}
	for sn, s := range c.Services {
		if s.Migrations == "" || len(s.Roles) == 0 || s.Pooled == "" {
			return fmt.Errorf("cells: service %s needs migrations, roles and a pooled DSN", sn)
		}
		for cn := range c.Cells {
			if s.Cells[cn] == "" {
				return fmt.Errorf("cells: service %s has no DSN for cell %s", sn, cn)
			}
		}
		for cn := range s.Cells {
			if _, ok := c.Cells[cn]; !ok {
				return fmt.Errorf("cells: service %s names cell %s, which is not in cells", sn, cn)
			}
		}
	}
	return nil
}

// ServiceNames lists the services in a stable order.
func (c Config) ServiceNames() []string {
	names := make([]string, 0, len(c.Services))
	for n := range c.Services {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// CellNames lists the cells in a stable order.
func (c Config) CellNames() []string {
	names := make([]string, 0, len(c.Cells))
	for n := range c.Cells {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Endpoint is the service's database in a cell ("" for the pooled database).
func (c Config) Endpoint(service, cell string) (pgxx.Endpoint, error) {
	s, ok := c.Services[service]
	if !ok {
		return pgxx.Endpoint{}, fmt.Errorf("cells: no service %q", service)
	}
	if cell == "" {
		return pgxx.Endpoint{MigratorDSN: s.Pooled}, nil
	}
	dsn, ok := s.Cells[cell]
	if !ok {
		return pgxx.Endpoint{}, fmt.Errorf("cells: service %s has no cell %q", service, cell)
	}
	return pgxx.Endpoint{Cell: cell, MigratorDSN: dsn}, nil
}

// Schemas is the tenant schema definition of a service: its migration files and roles.
func (c Config) Schemas(service string) (pgxx.TenantSchemas, error) {
	s, ok := c.Services[service]
	if !ok {
		return pgxx.TenantSchemas{}, fmt.Errorf("cells: no service %q", service)
	}
	fsys := os.DirFS(s.Migrations)
	if _, err := fs.Stat(fsys, "."); err != nil {
		return pgxx.TenantSchemas{}, fmt.Errorf("cells: migrations of %s: %w", service, err)
	}
	return pgxx.TenantSchemas{Migrations: fsys, Roles: s.Roles}, nil
}

// Region is the region recorded for a cell.
func (c Config) Region(cell string) string { return c.Cells[cell].Region }
