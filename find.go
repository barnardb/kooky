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

func concurrentlyVisitCookieStoreFinders(visit func(finder CookieStoreFinder)) {
	var wg sync.WaitGroup
	muFinder.RLock()
	defer muFinder.RUnlock()
	wg.Add(len(finders))
	for _, finder := range finders {
		go func(finder CookieStoreFinder) {
			defer wg.Done()
			visit(finder)
		}(finder)
	}
	wg.Wait()
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

	c := make(chan []CookieStore)
	done := make(chan struct{})

	go func() {
		for cookieStores := range c {
			ret = append(ret, cookieStores...)
		}
		close(done)
	}()

	concurrentlyVisitCookieStoreFinders(func(finder CookieStoreFinder) {
		cookieStores, err := finder.FindCookieStores()
		if err == nil && cookieStores != nil {
			c <- cookieStores
		}
	})

	close(c)
	<-done

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

	cs := make(chan []CookieStore)
	c := make(chan *Cookie)
	done := make(chan struct{})

	// append cookies
	go func() {
		for cookie := range c {
			ret = append(ret, cookie)
		}
		close(done)
	}()

	// read cookies
	go func() {
		var wgcs sync.WaitGroup
		for cookieStores := range cs {
			for _, store := range cookieStores {
				wgcs.Add(1)
				go func(store CookieStore) {
					defer wgcs.Done()
					store.VisitCookies(func(cookie *Cookie, initializeValue CookieValueInitializer) error {
						err := initializeValue(cookie)
						if err != nil {
							return err
						}
						c <- cookie
						return nil
					})
				}(store)
			}

		}
		wgcs.Wait()
		close(c)
	}()

	// find cookie stores
	concurrentlyVisitCookieStoreFinders(func(finder CookieStoreFinder) {
		cookieStores, err := finder.FindCookieStores()
		if err == nil && cookieStores != nil {
			cs <- cookieStores
		}
	})
	close(cs)

	<-done

	return ret
}
