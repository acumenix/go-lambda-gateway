package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/rpc"
	"os"
	"strconv"
	"time"
	"unicode"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda/messages"
)

var lambdaHost string

func IsBinary(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) {
			return true
		}
	}
	return false
}

func invokeLambda(request *events.APIGatewayProxyRequest) (*events.APIGatewayProxyResponse, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	invokeRequest := &messages.InvokeRequest{
		Payload:      payload,
		RequestId:    "0",
		XAmznTraceId: "",
		Deadline: messages.InvokeRequest_Timestamp{
			Seconds: int64(now.Unix()),
			Nanos:   int64(now.Nanosecond()),
		},
		InvokedFunctionArn:    "",
		CognitoIdentityId:     "",
		CognitoIdentityPoolId: "",
		ClientContext:         nil,
	}

	client, err := rpc.Dial("tcp", lambdaHost)
	if err != nil {
		return nil, err
	}
	var invokeResponse messages.InvokeResponse
	if err = client.Call("Function.Invoke", invokeRequest, &invokeResponse); err != nil {
		return nil, err
	}
	if invokeResponse.Error != nil {
		return nil, errors.New(invokeResponse.Error.Message)
	}

	var response events.APIGatewayProxyResponse
	err = json.Unmarshal(invokeResponse.Payload, &response)
	if err != nil {
		return nil, err
	}

	return &response, nil
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading body: %v", err)
		http.Error(w, "Error reading body", http.StatusBadRequest)
		return
	}

	request := &events.APIGatewayProxyRequest{
		Resource:   "/",
		Path:       r.URL.Path,
		HTTPMethod: r.Method,
		Headers: map[string]string{
			"Host": r.Host,
		},
		MultiValueHeaders: map[string][]string{
			"Host": []string{r.Host},
		},
		QueryStringParameters:           map[string]string{},
		MultiValueQueryStringParameters: map[string][]string{},
		PathParameters:                  nil,
		StageVariables:                  nil,
		RequestContext:                  events.APIGatewayProxyRequestContext{},
		Body:                            string(body),
		IsBase64Encoded:                 false,
	}
	if r.URL.Path != "/" {
		request.Resource = "/{proxy+}"
		request.PathParameters = map[string]string{
			"proxy": r.URL.Path[1:],
		}
	}
	for header, values := range r.Header {
		for _, value := range values {
			request.Headers[header] = value
			request.MultiValueHeaders[header] = append(request.MultiValueHeaders[header], value)
		}
	}
	for key, values := range r.URL.Query() {
		for _, value := range values {
			request.QueryStringParameters[key] = value
			request.MultiValueQueryStringParameters[key] = append(request.MultiValueQueryStringParameters[key], value)
		}
	}
	if IsBinary(request.Body) {
		request.IsBase64Encoded = true
		request.Body = base64.StdEncoding.EncodeToString(body)
	}

	response, err := invokeLambda(request)
	if err != nil {
		log.Printf("Error invoking lambda: %v", err)
		http.Error(w, "Error invoking lambda", http.StatusInternalServerError)
		return
	}
	// fmt.Printf("Response: %v\n", response)

	for header, value := range response.Headers {
		w.Header().Set(header, value)
	}
	w.WriteHeader(response.StatusCode)
	if response.IsBase64Encoded {
		bytes, err := base64.StdEncoding.DecodeString(response.Body)
		if err != nil {
			log.Printf("Error base64-decoding response body: %v", err)
			http.Error(w, "Error base64-decoding response body", http.StatusInternalServerError)
			return
		}
		w.Write(bytes)
	} else {
		fmt.Fprintf(w, response.Body)
	}

	// Log something similar to the common log format
	// host [date] request status bytes
	fmt.Printf("%s [%v] \"%s %s\" %v\n", r.Host, time.Now().Format("2006-01-02 15:04:05"), r.Method, r.URL.Path, len(response.Body))
}

func main() {
	lambdaHost = os.Getenv("LAMBDA_HOST")
	if lambdaHost == "" {
		lambdaHost = "localhost:8001"
	}
	fmt.Fprintf(os.Stderr, "Lambda address: %s\n", lambdaHost)

	port, _ := strconv.Atoi(os.Getenv("PORT"))
	if port == 0 {
		port = 8002
	}
	fmt.Fprintf(os.Stderr, "Listening on port: %d\n", port)
	fmt.Fprintln(os.Stderr)

	http.HandleFunc("/", handleRequest)
	err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
