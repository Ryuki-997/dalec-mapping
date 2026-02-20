package repository

type CRUDRequest string

const (
	GET    CRUDRequest = "GET"
	POST   CRUDRequest = "POST"
	PUT    CRUDRequest = "PUT"
	DELETE CRUDRequest = "DELETE"
)

type GithubRequest struct {
	URL     string
	Method  CRUDRequest
	Payload interface{}
}