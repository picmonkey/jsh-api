package jshapi

import (
	"fmt"
	"log"
	"net/http"
	"path"
	"strings"

	"goji.io/pat"

	"goji.io"

	"golang.org/x/net/context"

	"github.com/derekdowling/go-json-spec-handler"
	"github.com/derekdowling/jsh-api/store"
)

const (
	post   = "POST"
	get    = "GET"
	list   = "LIST"
	delete = "DELETE"
	patch  = "PATCH"
)

// Resource holds the necessary state for creating a REST API endpoint for a
// given resource type. Will be accessible via `/(prefix/)types` where the
// proceeding `prefix/` is only precent if it is not empty.
//
// Using NewCRUDResource you can generate a generic CRUD handler for a
// JSON Specification Resource end point. If you wish to only implement a subset
// of these endpoints that is also available through NewResource() and manually
// registering storage handlers via .Post(), .Get(), .List(), .Patch(), and .Delete():
//
// You can add your own routes using the goji.Mux API:
//
//	func searchHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
//		name := pat.Param(ctx, "name")
//		fmt.Fprintf(w, "Hello, %s!", name)
//	}
//
//	resource := jshapi.NewCRUDResource("user", userStorage)
//	// creates /users/search/:name
//	resource.HandleC(pat.New(resource.Matcher()+"/search/:name"), searchHandler)
//
// Or add a nested resources:
//
//	commentResource := resource.NewSubResource("post")
//	// creates /users/:id/posts* routes
//	resource.CRUD(postStorage)
type Resource struct {
	*goji.Mux
	// The singular name of the resource type("user", "post", etc)
	Type string
	// An implementation of Go's standard logger
	Logger *log.Logger
	// Prefix is set if the resource is not the top level of URI, "/prefix/resources
	Routes []string
	// Map of relationships
	Relationships map[string]Relationship
	prefix        string
}

// NewResource is a resource constructor that makes no assumptions about routes
// that you'd like to implement, but still provides some basic utilities for
// managing routes and handling API calls.
func NewResource(resourceType string) *Resource {
	return &Resource{
		Mux:           goji.NewMux(),
		Type:          resourceType,
		Relationships: map[string]Relationship{},
		Routes:        []string{},
		prefix:        "/",
	}
}

// NewCRUDResource generates a resource
func NewCRUDResource(resourceType string, storage store.CRUD) *Resource {
	resource := NewResource(resourceType)
	resource.CRUD(storage)
	return resource
}

// CRUD is syntactic sugar and a shortcut for registering all JSON API CRUD
// routes for a compatible storage implementation:
//
// Registers handlers for:
//	GET    /[prefix/]types
//	POST   /[prefix/]types
//	GET    /[prefix/]types/:id
//	DELETE /[prefix/]types/:id
//	PATCH  /[prefix/]types/:id
func (res *Resource) CRUD(storage store.CRUD) {
	res.Get(storage.Get)
	res.Patch(storage.Update)
	res.Post(storage.Save)
	res.List(storage.List)
	res.Delete(storage.Delete)
}

// Post registers a `POST /resources` handler with the resource
func (res *Resource) Post(storage store.Save) {
	res.HandleFuncC(
		pat.Post(res.Matcher()),
		func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
			res.postHandler(ctx, w, r, storage)
		},
	)

	res.addRoute(post, res.Matcher())
}

// Get registers a `GET /resources/:id` handler for the resource
func (res *Resource) Get(storage store.Get) {
	res.HandleFuncC(
		pat.Get(res.IDMatcher()),
		func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
			res.getHandler(ctx, w, r, storage)
		},
	)

	res.addRoute(get, res.IDMatcher())
}

// List registers a `GET /resources` handler for the resource
func (res *Resource) List(storage store.List) {
	res.HandleFuncC(
		pat.Get(res.Matcher()),
		func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
			res.listHandler(ctx, w, r, storage)
		},
	)

	res.addRoute(get, res.Matcher())
}

// Delete registers a `DELETE /resources/:id` handler for the resource
func (res *Resource) Delete(storage store.Delete) {
	res.HandleFuncC(
		pat.Delete(res.IDMatcher()),
		func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
			res.deleteHandler(ctx, w, r, storage)
		},
	)

	res.addRoute(delete, res.IDMatcher())
}

// Patch registers a `PATCH /resources/:id` handler for the resource
func (res *Resource) Patch(storage store.Update) {
	res.HandleFuncC(
		pat.Patch(res.IDMatcher()),
		func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
			res.patchHandler(ctx, w, r, storage)
		},
	)

	res.addRoute(patch, res.IDMatcher())
}

// ToOne handles the /resources/:id/(relationships/)<resourceType> route which
// represents a One-To-One relationship between the resource and the
// specified resourceType
func (res *Resource) ToOne(
	resourceType string,
	storage store.Get,
) {
	resourceType = strings.TrimSuffix(resourceType, "s")

	res.relationshipHandler(
		resourceType,
		func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
			res.getHandler(ctx, w, r, storage)
		},
	)

	res.Relationships[resourceType] = ToOne
}

// ToMany handles the /resources/:id/(relationships/)<resourceType>s route which
// represents a One-To-Many relationship between the resource and the
// specified resourceType
func (res *Resource) ToMany(
	resourceType string,
	storage store.ToMany,
) {
	if !strings.HasSuffix(resourceType, "s") {
		resourceType = fmt.Sprintf("%ss", resourceType)
	}

	res.relationshipHandler(
		resourceType,
		func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
			res.toManyHandler(ctx, w, r, storage)
		},
	)

	res.Relationships[resourceType] = ToMany
}

// relationshipHandler does the dirty work of setting up both routes for a single
// relationship
func (res *Resource) relationshipHandler(
	resourceType string,
	handler goji.HandlerFunc,
) {

	// handle /.../:id/<resourceType>
	matcher := fmt.Sprintf("%s/%s", res.IDMatcher(), resourceType)
	res.HandleFuncC(
		pat.Get(matcher),
		handler,
	)
	res.addRoute(get, matcher)

	// handle /.../:id/relationships/<resourceType>
	relationshipMatcher := fmt.Sprintf("%s/relationships/%s", res.IDMatcher(), resourceType)
	res.HandleFuncC(
		pat.Get(relationshipMatcher),
		handler,
	)
	res.addRoute(get, relationshipMatcher)
}

// Mutate allows you to add custom actions to your resource types, it uses the
// GET /(prefix/)resourceTypes/:id/<actionName> path format
func (res *Resource) Mutate(actionName string, storage store.Get) {
	matcher := path.Join(res.IDMatcher(), actionName)

	res.HandleFuncC(
		pat.Get(matcher),
		func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
			res.mutateHandler(ctx, w, r, storage)
		},
	)

	res.addRoute(patch, matcher)
}

// POST /resources
func (res *Resource) postHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, storage store.Save) {
	parsedObject, err := jsh.ParseObject(r)
	if err != nil {
		res.SendAndLog(ctx, w, r, err)
		return
	}

	object, err := storage(ctx, parsedObject)
	if err != nil {
		res.SendAndLog(ctx, w, r, err)
		return
	}

	res.SendAndLog(ctx, w, r, object)
}

// GET /resources/:id
func (res *Resource) getHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, storage store.Get) {
	id := pat.Param(ctx, "id")

	object, err := storage(ctx, id)
	if err != nil {
		res.SendAndLog(ctx, w, r, err)
		return
	}

	res.SendAndLog(ctx, w, r, object)
}

// GET /resources
func (res *Resource) listHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, storage store.List) {
	list, err := storage(ctx)
	if err != nil {
		res.SendAndLog(ctx, w, r, err)
		return
	}

	res.SendAndLog(ctx, w, r, list)
}

// DELETE /resources/:id
func (res *Resource) deleteHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, storage store.Delete) {
	id := pat.Param(ctx, "id")

	err := storage(ctx, id)
	if err != nil {
		res.SendAndLog(ctx, w, r, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// PATCH /resources/:id
func (res *Resource) patchHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, storage store.Update) {
	parsedObject, err := jsh.ParseObject(r)
	if err != nil {
		res.SendAndLog(ctx, w, r, err)
		return
	}

	object, err := storage(ctx, parsedObject)
	if err != nil {
		res.SendAndLog(ctx, w, r, err)
		return
	}

	res.SendAndLog(ctx, w, r, object)
}

// GET /resources/:id/(relationships/)<resourceType>s
func (res *Resource) toManyHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, storage store.ToMany) {
	id := pat.Param(ctx, "id")

	list, err := storage(ctx, id)
	if err != nil {
		res.SendAndLog(ctx, w, r, err)
		return
	}

	res.SendAndLog(ctx, w, r, list)
}

// All HTTP Methods for /resources/:id/<mutate>
func (res *Resource) mutateHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, storage store.Get) {
	id := pat.Param(ctx, "id")

	response, err := storage(ctx, id)
	if err != nil {
		res.SendAndLog(ctx, w, r, err)
		return
	}

	res.SendAndLog(ctx, w, r, response)
}

// SendAndLog is a jsh wrapper function that handles logging 500 errors and
// ensures that any errors that leak out of JSH are also captured
func (res *Resource) SendAndLog(ctx context.Context, w http.ResponseWriter, r *http.Request, sendable jsh.Sendable) {
	response, err := sendable.Prepare(r, true)
	if err != nil && response.HTTPStatus == http.StatusInternalServerError {
		res.Logger.Printf("Error: %s", err.Internal())
	}

	sendErr := jsh.SendResponse(w, r, response)
	if sendErr != nil {
		res.Logger.Print(err.Error())
	}
}

// PluralType returns the resource's name, but pluralized
func (res *Resource) PluralType() string {
	return res.Type + "s"
}

// IDMatcher returns a uri path matcher for the resource type
func (res *Resource) IDMatcher() string {
	return path.Join(res.Matcher(), ":id")
}

// Matcher returns the top level uri path matcher for the resource type
func (res *Resource) Matcher() string {
	return path.Join(res.prefix, res.PluralType())
}

func (res *Resource) addRoute(method string, route string) {
	res.Routes = append(res.Routes, fmt.Sprintf("%s - %s", method, route))
}

// RouteTree prints a recursive route tree based on what the resource, and
// all subresources have registered
func (res *Resource) RouteTree() string {
	var routes string

	for _, route := range res.Routes {
		routes = strings.Join([]string{routes, route}, "\n")
	}

	return routes
}