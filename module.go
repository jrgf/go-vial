package vial

import (
	"fmt"
	"net/http"
)

// Module registers one named application capability.
type Module interface {
	Name() string
	Register(*Registrar) error
}

// Registrar exposes the route registration surface available to modules.
type Registrar struct {
	app    *App
	module string
}

// Register stages modules and commits their routes atomically.
func (app *App) Register(modules ...Module) error {
	app.mu.RLock()
	built := app.built
	app.mu.RUnlock()
	if built {
		return fmt.Errorf("vial: application is already built")
	}

	names := make([]string, 0, len(modules))
	routes := make([]routeDefinition, 0)
	for _, module := range modules {
		if module == nil {
			return fmt.Errorf("vial: module cannot be nil")
		}

		name := module.Name()
		registrar := &Registrar{app: New(), module: name}
		if err := module.Register(registrar); err != nil {
			return fmt.Errorf("register module %q: %w", name, err)
		}
		names = append(names, name)
		routes = append(routes, registrar.close()...)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.built {
		return fmt.Errorf("vial: application is already built")
	}
	app.modules = append(app.modules, names...)
	app.routes = append(app.routes, routes...)
	return nil
}

// Use adds middleware to every route registered by the module.
func (registrar *Registrar) Use(middleware ...Middleware) {
	registrar.app.Use(middleware...)
}

// Group creates a module route group.
func (registrar *Registrar) Group(prefix string) *Group {
	return registrar.app.Group(prefix)
}

// Handle registers a module route.
func (registrar *Registrar) Handle(method, path string, handler Handler, options ...RouteOption) {
	registrar.app.Handle(method, path, handler, options...)
}

// HandleHTTP mounts a standard-library handler in the module.
func (registrar *Registrar) HandleHTTP(pattern string, handler http.Handler, options ...RouteOption) {
	registrar.app.HandleHTTP(pattern, handler, options...)
}

func (registrar *Registrar) close() []routeDefinition {
	registrar.app.mu.Lock()
	defer registrar.app.mu.Unlock()

	registrar.app.built = true
	middleware := append([]Middleware(nil), registrar.app.middleware...)
	routes := make([]routeDefinition, len(registrar.app.routes))
	for index, definition := range registrar.app.routes {
		definition.route.Module = registrar.module
		routeMiddleware := make([]Middleware, 0, len(middleware)+len(definition.middleware))
		routeMiddleware = append(routeMiddleware, middleware...)
		definition.middleware = append(routeMiddleware, definition.middleware...)
		routes[index] = definition
	}
	return routes
}
