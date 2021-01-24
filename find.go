package kooky

import (
	"sync"
)

// CookieValueInitializer initializes cookie values
//
// An initializer for a cookie value, used to defer expensive value
// initialization (such as decryption).
//
// After this function returns without an error, the cookie's valua is ready to
// be read. A cookie should not be passed to an initializer more than once, and
// doing so may corrupt the cookie value.
type CookieValueInitializer func(*Cookie) error

// CookieValueAlreadyInitialized does nothing to the cookies that are past to it.
func CookieValueAlreadyInitialized(*Cookie) error {
	return nil
}

var _ CookieValueInitializer = CookieValueAlreadyInitialized

// CookieVisitor is a function that visits cookies.
//
// The visitor should pass the cookie to the value initializer before reading
// its value. This allows the visitor to avoid initializing the value if it
// doesn't need to read the the value, which can greatly improve performance
// for stores with encrypted values (such as Chrome's cookie database).
//
// If the visitor returns an error, the caller will stop visiting cookies and
// propagate the error.
type CookieVisitor func(*Cookie, CookieValueInitializer) error

// CookieStore represents a file, directory, etc containing cookies.
//
// Call CookieStore.Close() after using any of its methods.
type CookieStore interface {
	VisitCookies(CookieVisitor) error
	ReadCookies(...Filter) ([]*Cookie, error)
	Browser() string
	Profile() string
	IsDefaultProfile() bool
	FilePath() string
	Close() error
}

// CookieStoreFinder tries to find cookie stores at default locations.
type CookieStoreFinder interface {
	FindCookieStores() ([]CookieStore, error)
}

var (
	finders  = map[string]CookieStoreFinder{}
	muFinder sync.RWMutex
)

// RegisterFinder() registers CookieStoreFinder enabling automatic finding of
// cookie stores with FindAllCookieStores() and ReadCookies().
//
// RegisterFinder() is called by init() in the browser subdirectories.
func RegisterFinder(browser string, finder CookieStoreFinder) {
	muFinder.Lock()
	defer muFinder.Unlock()
	if finder != nil {
		finders[browser] = finder
	}
}

// ConcurrentlyVisitFinders invokes the visitor for each registered CookieStoreFinder
//
// The vistor may be invoked concurrently. ConcurrentlyVisitFinders waits until
// all vistor invocations have completed before returning.
func ConcurrentlyVisitFinders(visitor func(name string, finder CookieStoreFinder)) {
	var wg sync.WaitGroup
	muFinder.RLock()
	defer muFinder.RUnlock()
	wg.Add(len(finders))
	for name, finder := range finders {
		go func(name string, finder CookieStoreFinder) {
			defer wg.Done()
			visitor(name, finder)
		}(name, finder)
	}
	wg.Wait()
}

// ConcurrentlyVisitStores invokes the visitor CookieStore found by the finder
//
// The vistor may be invoked concurrently. ConcurrentlyVisitStores waits until
// all vistor invocations have completed before returning.
func ConcurrentlyVisitStores(finder CookieStoreFinder, visit func(CookieStore)) error {
	cookieStores, err := finder.FindCookieStores()
	if err != nil || cookieStores == nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(len(cookieStores))
	for _, cookieStore := range cookieStores {
		go func(cookieStore CookieStore) {
			defer wg.Done()
			visit(cookieStore)
		}(cookieStore)
	}
	wg.Wait()
	return nil
}

// FindAllCookieStores() tries to find cookie stores at default locations.
//
// FindAllCookieStores() requires registered CookieStoreFinders.
//
// Register cookie store finders for all browsers like this:
//
//  import _ "github.com/zellyn/kooky/allbrowsers"
//
// Or only a specific browser:
//
//  import _ "github.com/zellyn/kooky/chrome"
func FindAllCookieStores() []CookieStore {
	var ret []CookieStore
	cookieStores := make(chan CookieStore)
	go func() {
		ConcurrentlyVisitFinders(func(name string, finder CookieStoreFinder) {
			ConcurrentlyVisitStores(finder, func(store CookieStore) {
				cookieStores <- store
			})
		})
		close(cookieStores)
	}()
	for cookieStore := range cookieStores {
		ret = append(ret, cookieStore)
	}
	return ret
}

// ReadCookies() uses registered cookiestore finders to read cookies.
// Erronous reads are skipped.
//
// Register cookie store finders for all browsers like this:
//
//  import _ "github.com/zellyn/kooky/allbrowsers"
//
// Or only a specific browser:
//
//  import _ "github.com/zellyn/kooky/chrome"
func ReadCookies(filters ...Filter) []*Cookie {
	var ret []*Cookie
	cookies := make(chan *Cookie)
	go func() {
		ConcurrentlyVisitFinders(func(name string, finder CookieStoreFinder) {
			ConcurrentlyVisitStores(finder, func(store CookieStore) {
				store.VisitCookies(func(cookie *Cookie, initializeValue CookieValueInitializer) error {
					if err := initializeValue(cookie); err != nil {
						return err
					}
					if FilterCookie(cookie, filters...) {
						cookies <- cookie
					}
					return nil
				})
			})
		})
		close(cookies)
	}()
	for cookie := range cookies {
		ret = append(ret, cookie)
	}
	return ret
}
