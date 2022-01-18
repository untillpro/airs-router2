/*
 * Copyright (c) 2022-present unTill Pro, Ltd.
 */

package router2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	iblobstorage "github.com/heeus/core-iblobstorage"
	iprocbus "github.com/heeus/core-iprocbus"
	istructs "github.com/heeus/core-istructs"
	ibus "github.com/untillpro/airs-ibus"
)

type blobWriteDetails struct {
	name     string
	mimeType string
}

type blobReadDetails struct {
	blobID istructs.RecordID
}

type blobBaseMessage struct {
	req            *http.Request
	resp           http.ResponseWriter
	principalToken string
	doneChan       chan struct{}
	wsid           istructs.WSID
}

type blobMessage struct {
	blobBaseMessage
	blobDetails interface{}
}

func (bm *blobBaseMessage) Release() {
	bm.req.Body.Close()
}

func blobReadMessageHandler(bbm blobBaseMessage, blobReadDetails blobReadDetails, clusterAppBlobberID istructs.ClusterAppID, blobStorage iblobstorage.IBLOBStorage) {
	defer close(bbm.doneChan)

	// request to HVM to check the principalToken
	req := ibus.Request{
		Method:   ibus.HTTPMethodPOST,
		WSID:     int64(bbm.wsid),
		AppQName: istructs.AppQName_sys_blobber.String(),
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
		AppID: clusterAppBlobberID,
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
		writeTextResponse(bbm.resp, err.Error(), http.StatusInternalServerError)
	}
}

func blobWriteMessageHandler(bbm blobBaseMessage, blobWriteDetails blobWriteDetails, clusterAppBlobberID istructs.ClusterAppID, blobStorage iblobstorage.IBLOBStorage) {
	defer close(bbm.doneChan)

	// request HVM for check the principalToken and get a blobID
	req := ibus.Request{
		Method:   ibus.HTTPMethodPOST,
		WSID:     int64(bbm.wsid),
		AppQName: "sys/blob",
		Resource: "c.sys.uploadBLOBHelper",
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
	cmdResp := map[string]interface{}{}
	if err := json.Unmarshal(blobHelperResp.Data, &cmdResp); err != nil {
		return
	}
	blobID := int64(cmdResp["newIDs"].(map[string]interface{})["1"].(float64))

	// write the BLOB
	key := iblobstorage.KeyType{
		AppID: clusterAppBlobberID,
		WSID:  bbm.wsid,
		ID:    istructs.RecordID(blobID),
	}
	descr := iblobstorage.DescrType{
		Name:     blobWriteDetails.name,
		MimeType: blobWriteDetails.mimeType,
	}

	if err := blobStorage.WriteBLOB(bbm.req.Context(), key, descr, bbm.req.Body, math.MaxInt64); err != nil {
		writeTextResponse(bbm.resp, err.Error(), http.StatusInternalServerError)
		return
	}
	writeTextResponse(bbm.resp, strconv.Itoa(int(key.ID)), http.StatusOK)
}

// ctx here is HVM context. It used to track HVM shutdown. Blobber will use the request's context
func blobMessageHandler(ctx context.Context, sc iprocbus.ServiceChannel, clusterAppBlobberID istructs.ClusterAppID, blobStorage iblobstorage.IBLOBStorage) {
	for ctx.Err() == nil {
		select {
		case mesIntf := <-sc:
			blobMessage := mesIntf.(blobMessage)
			switch blobDetails := blobMessage.blobDetails.(type) {
			case blobReadDetails:
				blobReadMessageHandler(blobMessage.blobBaseMessage, blobDetails, clusterAppBlobberID, blobStorage)
			case blobWriteDetails:
				blobWriteMessageHandler(blobMessage.blobBaseMessage, blobDetails, clusterAppBlobberID, blobStorage)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *httpService) blobRequestHandler(resp http.ResponseWriter, req *http.Request, details interface{}) {
	vars := mux.Vars(req)
	wsid, _ := strconv.ParseInt(vars[wSIDVar], 10, 64) // error impossible, checked by router url rule
	mes := blobMessage{
		blobBaseMessage: blobBaseMessage{
			req:            req,
			resp:           resp,
			principalToken: vars["principalToken"],
			wsid:           istructs.WSID(wsid),
			doneChan:       make(chan struct{}),
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
		blobID, _ := strconv.ParseInt(vars["blobID"], 10, 64) // error impossible, checked by router url rule
		blobReadDetails := blobReadDetails{
			blobID: istructs.RecordID(blobID),
		}
		s.blobRequestHandler(resp, req, blobReadDetails)
	}
}

func (s *httpService) blobWriteRequestHandler() http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		vars := mux.Vars(req)
		blobWriteDetails := blobWriteDetails{
			name:     vars["name"],
			mimeType: vars["mimeType"],
		}
		s.blobRequestHandler(resp, req, blobWriteDetails)
	}
}
