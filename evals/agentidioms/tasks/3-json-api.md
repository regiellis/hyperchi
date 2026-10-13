Add a JSON API for books: GET /api/books returns every book, and GET /api/books/{id}
returns one book by its numeric id. An unknown or non-numeric id must return a JSON
error with the right HTTP status code. Define the books in Go.
