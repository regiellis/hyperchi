package publishing_test

import (
	"fmt"
	"net/http/httptest"

	"github.com/regiellis/hyperchi/hyperchi/publishing"
)

func ExampleSitemap() {
	sitemap, err := publishing.Sitemap([]publishing.SitemapEntry{
		{URL: "https://example.com/"},
		{URL: "https://example.com/about"},
	})
	if err != nil {
		panic(err)
	}
	response := httptest.NewRecorder()
	sitemap.Handler().ServeHTTP(response, httptest.NewRequest("HEAD", "/sitemap.xml", nil))
	fmt.Println(response.Code)
	fmt.Println(response.Header().Get("Content-Type"))
	fmt.Println(response.Body.Len())
	// Output:
	// 200
	// application/xml; charset=utf-8
	// 0
}
