package server

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"

	"github.com/pierrre/imageserver"
	imageserver_source "github.com/pierrre/imageserver/source"
)

// Server is a imageserver.Server implementation that gets the Image from an HTTP URL.
//
// It parses the "source" param as URL, then do a GET request.
// It returns an error if the HTTP status code is not 200 (OK).
type Server struct {
	// Client is an optional HTTP client.
	// http.DefaultClient is used by default.
	Client *http.Client

	// Identify identifies the Image format.
	// By default, it uses IdentifyHeader().
	Identify func(resp *http.Response, data []byte) (format string, err error)
}

// Get implements imageserver.Server.
func (srv *Server) Get(params imageserver.Params) (*imageserver.Image, error) {
	resp, err := srv.doRequest(params)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = resp.Body.Close()
	}()
	data, err := loadData(resp)
	if err != nil {
		return nil, err
	}
	format, err := srv.identify(resp, data)
	if err != nil {
		return nil, err
	}
	return &imageserver.Image{
		Format: format,
		Data:   data,
	}, nil
}

func (srv *Server) doRequest(params imageserver.Params) (*http.Response, error) {
	src, err := params.GetString(imageserver_source.Param)
	if err != nil {
		return nil, err
	}

	svc := s3.New(session.New(), &aws.Config{Region: aws.String("us-east-1"), S3ForcePathStyle: aws.Bool(true)})

	awsParams := &s3.GetObjectInput{
		Bucket: aws.String("parishconnect-bkt"),
		Key:    aws.String(src),
	}

	objReq, _ := svc.GetObjectRequest(awsParams)

	url, err := objReq.Presign(5 * time.Minute)
	if err != nil {
		return nil, newSourceError(err.Error())
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, newSourceError(err.Error())
	}
	c := srv.Client
	if c == nil {
		c = http.DefaultClient
	}
	response, err := c.Do(req)
	if err != nil {
		return nil, newSourceError(err.Error())
	}
	return response, nil
}

func loadData(resp *http.Response) ([]byte, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, newSourceError(fmt.Sprintf("HTTP status code %d while downloading", resp.StatusCode))
	}
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, newSourceError(fmt.Sprintf("error while downloading: %s", err))
	}
	return data, nil
}

func (srv *Server) identify(resp *http.Response, data []byte) (format string, err error) {
	idf := srv.Identify
	if idf == nil {
		idf = IdentifyHeader
	}
	format, err = idf(resp, data)
	if err != nil {
		return "", newSourceError(fmt.Sprintf("unable to identify image format: %s", err.Error()))
	}
	return format, nil
}

func newSourceError(msg string) error {
	return &imageserver.ParamError{
		Param:   imageserver_source.Param,
		Message: msg,
	}
}

// IdentifyHeader identifies the Image format with the "Content-Type" header.
func IdentifyHeader(resp *http.Response, data []byte) (format string, err error) {
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		return "", fmt.Errorf("no HTTP \"Content-Type\" header")
	}
	const pref = "image/"
	if !strings.HasPrefix(ct, pref) {
		return "", fmt.Errorf("HTTP \"Content-Type\" header does not begin with \"%s\": %s", pref, ct)
	}
	return ct[len(pref):], nil
}
