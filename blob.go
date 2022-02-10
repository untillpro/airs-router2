/*
 * Copyright (c) 2022-present unTill Pro, Ltd.
 */

package router2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gorilla/mux"
	iblobstorage "github.com/heeus/core-iblobstorage"
	iprocbus "github.com/heeus/core-iprocbus"
	istructs "github.com/heeus/core-istructs"
	ibus "github.com/untillpro/airs-ibus"
)

type blobWriteDetailsSingle struct {
	name     string
	mimeType string
}

type blobWriteDetailsMultipart struct {
	boundary string
}

type blobReadDetails struct {
	blobID istructs.RecordID
}

type blobBaseMessage struct {
	req                 *http.Request
	resp                http.ResponseWriter
	principalToken      string
	doneChan            chan struct{}
	wsid                istructs.WSID
	appQName            istructs.AppQName
	header              map[string][]string
	clusterAppBlobberID istructs.ClusterAppID
	blobMaxSize         BLOBMaxSizeType
}

type blobMessage struct {
	blobBaseMessage
	blobDetails interface{}
}

func (bm *blobBaseMessage) Release() {
	bm.req.Body.Close()
}

func blobReadMessageHandler(bbm blobBaseMessage, blobReadDetails blobReadDetails, blobStorage iblobstorage.IBLOBStorage) {
	defer close(bbm.doneChan)

	// request to HVM to check the principalToken
	req := ibus.Request{
		Method:   ibus.HTTPMethodPOST,
		WSID:     int64(bbm.wsid),
		AppQName: bbm.appQName.String(),
		Resource: "c.sys.downloadBLOBHelper",
		Body:     []byte(`{"args": {"principalToken":"` + bbm.principalToken + `"}}`),
	}
	blobHelperResp, _, _, err := ibus.SendRequest2(bbm.req.Context(), req, busTimeout)
	if err != nil {
		writeTextResponse(bbm.resp, "failed to exec c.sys.uploadBLOBHelper: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if blobHelperResp.StatusCode != http.StatusOK {
		writeTextResponse(bbm.resp, "c.sys.uploadBLOBHelper returned error: "+string(blobHelperResp.Data), http.StatusInternalServerError)
		return
	}

	// read the BLOB
	key := iblobstorage.KeyType{
		AppID: bbm.clusterAppBlobberID,
		WSID:  bbm.wsid,
		ID:    blobReadDetails.blobID,
	}
	stateWriterDiscard := func(state iblobstorage.BLOBState) error {
		if state.Status != iblobstorage.BLOBStatus_Completed {
			return errors.New("blob is not completed")
		}
		if len(state.Error) > 0 {
			return errors.New(state.Error)
		}
		setContentType(bbm.resp, state.Descr.MimeType)
		bbm.resp.Header().Add("Content-Disposition", fmt.Sprintf(`attachment;filename="%s"`, state.Descr.Name))
		bbm.resp.WriteHeader(http.StatusOK)
		return nil
	}
	if err := blobStorage.ReadBLOB(bbm.req.Context(), key, stateWriterDiscard, bbm.resp); err != nil {
		if err == iblobstorage.ErrBLOBNotFound {
			writeTextResponse(bbm.resp, err.Error(), http.StatusNotFound)
			return
		}
		writeTextResponse(bbm.resp, err.Error(), http.StatusInternalServerError)
	}
}

func writeBLOB(ctx context.Context, wsid int64, appQName string, header map[string][]string, principalToken string, resp http.ResponseWriter,
	clusterAppBlobberID istructs.ClusterAppID, blobName, blobMimeType string, blobStorage iblobstorage.IBLOBStorage, body io.ReadCloser,
	blobMaxSize int64) (blobID int64) {
	// request HVM for check the principalToken and get a blobID
	req := ibus.Request{
		Method:   ibus.HTTPMethodPOST,
		WSID:     int64(wsid),
		AppQName: appQName,
		Resource: "c.sys.uploadBLOBHelper",
		Body:     []byte(`{"args": {"principalToken":"` + principalToken + `"}}`),
		Header:   header,
	}
	blobHelperResp, _, _, err := ibus.SendRequest2(ctx, req, busTimeout)
	if err != nil {
		writeTextResponse(resp, "failed to exec c.sys.uploadBLOBHelper: "+err.Error(), http.StatusInternalServerError)
		return 0
	}
	if blobHelperResp.StatusCode != http.StatusOK {
		writeTextResponse(resp, "c.sys.uploadBLOBHelper returned error: "+string(blobHelperResp.Data), http.StatusInternalServerError)
		return 0
	}
	cmdResp := map[string]interface{}{}
	if err := json.Unmarshal(blobHelperResp.Data, &cmdResp); err != nil {
		writeTextResponse(resp, "failed to json-unmarshal c.sys.uploadBLOBHelper result: "+err.Error(), http.StatusInternalServerError)
		return 0
	}
	newIDs := cmdResp["newIDs"].(map[string]interface{})

	blobID = int64(newIDs["1"].(float64))
	// write the BLOB
	key := iblobstorage.KeyType{
		AppID: clusterAppBlobberID,
		WSID:  istructs.WSID(wsid),
		ID:    istructs.RecordID(blobID),
	}
	descr := iblobstorage.DescrType{
		Name:     blobName,
		MimeType: blobMimeType,
	}

	if err := blobStorage.WriteBLOB(ctx, key, descr, body, blobMaxSize); err != nil {
		if err == iblobstorage.ErrBLOBSizeQuotaExceeded {
			writeTextResponse(resp, fmt.Sprintf("blob size quouta exceeded (max %d allowed)", blobMaxSize), http.StatusForbidden)
			return 0
		}
		writeTextResponse(resp, err.Error(), http.StatusInternalServerError)
		return 0
	}
	return blobID
}

func blobWriteMessageHandlerMultipart(bbm blobBaseMessage,
	blobStorage iblobstorage.IBLOBStorage, header map[string][]string, boundary string) {
	defer close(bbm.doneChan)

	r := multipart.NewReader(bbm.req.Body, boundary)
	part, err := r.NextPart()
	if err == io.EOF {
		writeTextResponse(bbm.resp, "empty multipart request", http.StatusBadRequest)
		return
	}
	if err != nil {
		writeTextResponse(bbm.resp, "failed to parse multipart: "+err.Error(), http.StatusBadRequest)
		return
	}
	partNum := 1 // will process the first part only
	contentDisposition := part.Header.Get("Content-Disposition")
	mediaType, params, err := mime.ParseMediaType(contentDisposition)
	if err != nil {
		writeTextResponse(bbm.resp, fmt.Sprintf("failed to parse Content-Disposition of part number %d: %s", partNum, contentDisposition), http.StatusBadRequest)
	}
	if mediaType != "form-data" {
		writeTextResponse(bbm.resp, fmt.Sprintf("unsupported ContentDisposition mediaType of part number %d: %s", partNum, mediaType), http.StatusBadRequest)
	}
	contentType := part.Header.Get("Content-Type")
	if len(contentType) == 0 {
		contentType = "application/x-binary"
	}

	blobID := writeBLOB(bbm.req.Context(), int64(bbm.wsid), bbm.appQName.String(), part.Header, bbm.principalToken, bbm.resp, bbm.clusterAppBlobberID,
		params["name"], contentType, blobStorage, part, int64(bbm.blobMaxSize))
	if blobID == 0 {
		return // request handled
	}
	writeTextResponse(bbm.resp, fmt.Sprint(blobID), http.StatusOK)
}

func blobWriteMessageHandlerSingle(bbm blobBaseMessage, blobWriteDetails blobWriteDetailsSingle, blobStorage iblobstorage.IBLOBStorage, header map[string][]string) {
	defer close(bbm.doneChan)

	blobID := writeBLOB(bbm.req.Context(), int64(bbm.wsid), bbm.appQName.String(), header, bbm.principalToken, bbm.resp, bbm.clusterAppBlobberID, blobWriteDetails.name,
		blobWriteDetails.mimeType, blobStorage, bbm.req.Body, int64(bbm.blobMaxSize))
	if blobID > 0 {
		writeTextResponse(bbm.resp, fmt.Sprint(blobID), http.StatusOK)
	}

}

// ctx here is HVM context. It used to track HVM shutdown. Blobber will use the request's context
func blobMessageHandler(hvmCtx context.Context, sc iprocbus.ServiceChannel, blobStorage iblobstorage.IBLOBStorage) {
	for hvmCtx.Err() == nil {
		select {
		case mesIntf := <-sc:
			blobMessage := mesIntf.(blobMessage)
			switch blobDetails := blobMessage.blobDetails.(type) {
			case blobReadDetails:
				blobReadMessageHandler(blobMessage.blobBaseMessage, blobDetails, blobStorage)
			case blobWriteDetailsSingle:
				blobWriteMessageHandlerSingle(blobMessage.blobBaseMessage, blobDetails, blobStorage, blobMessage.header)
			case blobWriteDetailsMultipart:
				blobWriteMessageHandlerMultipart(blobMessage.blobBaseMessage, blobStorage, blobMessage.header, blobDetails.boundary)
			}
		case <-hvmCtx.Done():
			return
		}
	}
}

func (s *httpService) blobRequestHandler(resp http.ResponseWriter, req *http.Request, principalToken string, details interface{}) {
	vars := mux.Vars(req)
	wsid, _ := strconv.ParseInt(vars[wSIDVar], 10, 64) // error impossible, checked by router url rule
	mes := blobMessage{
		blobBaseMessage: blobBaseMessage{
			req:                 req,
			resp:                resp,
			principalToken:      principalToken,
			wsid:                istructs.WSID(wsid),
			doneChan:            make(chan struct{}),
			appQName:            istructs.NewAppQName(vars[bp3AppOwner], vars[bp3AppName]),
			header:              req.Header,
			clusterAppBlobberID: s.ClusterAppBlobberID,
			blobMaxSize:         s.BLOBMaxSize,
		},
		blobDetails: details,
	}
	if !s.BlobberParams.procBus.Submit(0, 0, mes) {
		resp.WriteHeader(http.StatusServiceUnavailable)
		resp.Header().Add("Retry-After", fmt.Sprint(s.RetryAfterSecondsOn503))
		return
	}
	select {
	case <-mes.doneChan:
	case <-req.Context().Done():
	}
}

func (s *httpService) blobReadRequestHandler() http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		vars := mux.Vars(req)
		blobID, _ := strconv.ParseInt(vars[bp3BLOBID], 10, 64) // error impossible, checked by router url rule
		principalToken := headerOrCookieAuth(resp, req)
		if len(principalToken) == 0 {
			return
		}
		blobReadDetails := blobReadDetails{
			blobID: istructs.RecordID(blobID),
		}
		s.blobRequestHandler(resp, req, principalToken, blobReadDetails)
	}
}

func (s *httpService) blobWriteRequestHandler() http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		principalToken, isHandled := headerAuth(resp, req)
		if len(principalToken) == 0 {
			if !isHandled {
				writeUnauthorized(resp)
			}
			return
		}

		name, mimeType, boundary, ok := getBlobParams(resp, req)
		if !ok {
			return
		}

		if len(name) > 0 {
			s.blobRequestHandler(resp, req, principalToken, blobWriteDetailsSingle{
				name:     name,
				mimeType: mimeType,
			})
		} else {
			s.blobRequestHandler(resp, req, principalToken, blobWriteDetailsMultipart{
				boundary: boundary,
			})
		}
	}
}

func headerAuth(rw http.ResponseWriter, req *http.Request) (principalToken string, isHandled bool) {
	authHeader := req.Header.Get("Authorization")
	if len(authHeader) > 0 {
		if len(authHeader) < bearerPrefixLen || authHeader[:bearerPrefixLen] != bearerPrefix {
			writeUnauthorized(rw)
			return "", true
		}
		return authHeader[bearerPrefixLen:], false
	}
	return "", false
}

func headerOrCookieAuth(rw http.ResponseWriter, req *http.Request) (principalToken string) {
	principalToken, isHandled := headerAuth(rw, req)
	if isHandled {
		return ""
	}
	if len(principalToken) > 0 {
		return principalToken
	}
	for _, c := range req.Cookies() {
		if c.Name == "Authorization" {
			val, err := url.QueryUnescape(c.Value)
			if err != nil {
				writeTextResponse(rw, "failed to unescape cookie '"+c.Value+"'", http.StatusBadRequest)
				return ""
			}
			if len(val) < bearerPrefixLen || val[:bearerPrefixLen] != bearerPrefix {
				writeUnauthorized(rw)
				return ""
			}
			return val[bearerPrefixLen:]
		}
	}
	writeUnauthorized(rw)
	return ""
}

// determines BLOBs write kind: name+mimeType in query params -> single BLOB, body is BLOB content, otherwise -> multiple BLOBs, body is multipart/form-data
func getBlobParams(rw http.ResponseWriter, req *http.Request) (name, mimeType, boundary string, ok bool) {
	badRequest := func(msg string) {
		writeTextResponse(rw, msg, http.StatusBadRequest)
	}
	values := req.URL.Query()
	nameQuery, isSingleBLOB := values["name"]
	mimeTypeQuery, ok := values["mimeType"]
	if (isSingleBLOB && !ok) || (!isSingleBLOB && ok) {
		badRequest("both name and mimeType query params must be specified")
		return
	}

	contentType := req.Header.Get("Content-Type")
	if isSingleBLOB {
		if len(contentType) > 0 {
			badRequest("name+mimeType query params and multipart/form-data Content-Type header are mutual exclusive")
			return
		}
		name = nameQuery[0]
		mimeType = mimeTypeQuery[0]
		ok = true
		return
	}
	if len(contentType) == 0 {
		badRequest(`neither "name"+"mimeType" query params nor Content-Type header is not provided`)
		return
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		badRequest("failed ot parse Content-Type header: " + contentType)
		return
	}
	if mediaType != "multipart/form-data" {
		badRequest("unsupported Content-Type: " + contentType)
		return
	}
	boundary = params["boundary"]
	if len(boundary) == 0 {
		badRequest("boundary of multipart/form-data is not specified")
		return
	}
	ok = true
	return
}
