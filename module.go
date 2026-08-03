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
	root   *App
	module string
}

type moduleRegistration struct {
	routes     []routeDefinition
	startHooks []LifecycleHook
	stopHooks  []LifecycleHook
	tasks      []taskDefinition
}

// Register stages modules and commits their routes atomically.
func (app *App) Register(modules ...Module) error {
	app.mu.RLock()
	built := app.state >= applicationBuilt
	app.mu.RUnlock()
	if built {
		return fmt.Errorf("vial: application is already built")
	}

	names := make([]string, 0, len(modules))
	routes := make([]routeDefinition, 0)
	startHooks := make([]LifecycleHook, 0)
	stopHooks := make([]LifecycleHook, 0)
	tasks := make([]taskDefinition, 0)
	for _, module := range modules {
		if module == nil {
			return fmt.Errorf("vial: module cannot be nil")
		}

		name := module.Name()
		registrar := &Registrar{app: New(), root: app, module: name}
		if err := module.Register(registrar); err != nil {
			return fmt.Errorf("register module %q: %w", name, err)
		}
		registration := registrar.close()
		names = append(names, name)
		routes = append(routes, registration.routes...)
		startHooks = append(startHooks, registration.startHooks...)
		stopHooks = append(stopHooks, registration.stopHooks...)
		tasks = append(tasks, registration.tasks...)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.state >= applicationBuilt {
		return fmt.Errorf("vial: application is already built")
	}
	if len(modules) > 0 {
		app.state = applicationRegistering
	}
	app.modules = append(app.modules, names...)
	app.routes = append(app.routes, routes...)
	app.startHooks = append(app.startHooks, startHooks...)
	app.stopHooks = append(app.stopHooks, stopHooks...)
	app.tasks = append(app.tasks, tasks...)
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

// OnStart registers module startup hooks.
func (registrar *Registrar) OnStart(hooks ...LifecycleHook) {
	registrar.app.OnStart(hooks...)
}

// OnStop registers module shutdown hooks.
func (registrar *Registrar) OnStop(hooks ...LifecycleHook) {
	registrar.app.OnStop(hooks...)
}

// Go registers a supervised module background task.
func (registrar *Registrar) Go(name string, task Task, options ...TaskOption) {
	registrar.app.Go(registrar.module+"."+name, task, options...)
}

// Health registers a module liveness endpoint.
func (registrar *Registrar) Health(path string) {
	registrar.app.Health(path)
}

// Readiness registers a module readiness endpoint.
func (registrar *Registrar) Readiness(path string, checks ...HealthCheck) {
	registrar.app.Get(path, registrar.root.readinessHandler(checks...))
}

func (registrar *Registrar) close() moduleRegistration {
	registrar.app.mu.Lock()
	defer registrar.app.mu.Unlock()

	registrar.app.state = applicationBuilt
	middleware := append([]Middleware(nil), registrar.app.middleware...)
	routes := make([]routeDefinition, len(registrar.app.routes))
	for index, definition := range registrar.app.routes {
		definition.route.Module = registrar.module
		routeMiddleware := make([]Middleware, 0, len(middleware)+len(definition.middleware))
		routeMiddleware = append(routeMiddleware, middleware...)
		definition.middleware = append(routeMiddleware, definition.middleware...)
		routes[index] = definition
	}
	return moduleRegistration{
		routes:     routes,
		startHooks: append([]LifecycleHook(nil), registrar.app.startHooks...),
		stopHooks:  append([]LifecycleHook(nil), registrar.app.stopHooks...),
		tasks:      append([]taskDefinition(nil), registrar.app.tasks...),
	}
}
