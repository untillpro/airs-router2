/*
 * Copyright (c) 2022-present unTill Pro, Ltd.
 */

package router2

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	in10n "github.com/heeus/core-in10n"
	logger "github.com/heeus/core-logger"
	istructs "github.com/heeus/core/istructs"
)

/*
curl -G --data-urlencode "payload={\"SubjectLogin\": \"paa\", \"ProjectionKey\":[{\"App\":\"Application\",\"Projection\":\"paa.price\",\"WS\":1}, {\"App\":\"Application\",\"Projection\":\"paa.wine_price\",\"WS\":1}]}" https://alpha2.dev.untill.ru/n10n/channel -H "Content-Type: application/json"
*/
func (s *httpService) subscribeAndWatchHandler() http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		var (
			urlParams createChannelParamsType
			channel   in10n.ChannelID
			flusher   http.Flusher
			err       error
		)
		rw.Header().Set("Content-Type", "text/event-stream")
		rw.Header().Set("Cache-Control", "no-cache")
		rw.Header().Set("Connection", "keep-alive")
		jsonParam, ok := req.URL.Query()["payload"]
		if !ok || len(jsonParam[0]) < 1 {
			log.Println("Query parameter with payload (SubjectLogin id and ProjectionKey) is missing.")
			err = errors.New("query parameter with payload (SubjectLogin id and ProjectionKey) is missing")
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		err = json.Unmarshal([]byte(jsonParam[0]), &urlParams)
		if err != nil {
			log.Println(err)
			err = fmt.Errorf("cannot unmarshal input payload %w", err)
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		logger.Info("n10n subscribeAndWatch: ", urlParams)
		flusher, ok = rw.(http.Flusher)
		if !ok {
			http.Error(rw, "Streaming unsupported!", http.StatusInternalServerError)
			return
		}
		channel, err = s.n10n.NewChannel(urlParams.SubjectLogin, 24*time.Hour)
		if err != nil {
			log.Println(err)
			http.Error(rw, "Error create new channel", http.StatusTooManyRequests)
			return
		}
		if _, err = fmt.Fprintf(rw, "event: channelId\ndata: %s\n\n", channel); err != nil {
			log.Println("failed to write created channel id to client:", err)
			return
		}
		flusher.Flush()
		for _, projection := range urlParams.ProjectionKey {
			err = s.n10n.Subscribe(channel, projection)
			if err != nil {
				log.Println(err)
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		ch := make(chan UpdateUnit)
		go func() {
			defer close(ch)
			s.n10n.WatchChannel(req.Context(), channel, func(projection in10n.ProjectionKey, offset istructs.Offset) {
				var unit = UpdateUnit{
					Projection: projection,
					Offset:     offset,
				}
				ch <- unit
			})
		}()
		for req.Context().Err() == nil {
			var (
				projection, offset []byte
			)
			result, ok := <-ch
			if !ok {
				logger.Info("watch done")
				break
			}
			projection, err = json.Marshal(&result.Projection)
			if err == nil {
				if _, err = fmt.Fprintf(rw, "event: %s\n", projection); err != nil {
					log.Println("failed to write projection key event to client:", err)
				}
			}
			offset, _ = json.Marshal(&result.Offset) // error impossible
			if _, err = fmt.Fprintf(rw, "data: %s\n\n", offset); err != nil {
				log.Println("failed to write projection key offset to client:", err)
			}
			flusher.Flush()
		}
	}
}

/*
curl -G --data-urlencode "payload={\"Channel\": \"a23b2050-b90c-4ed1-adb7-1ecc4f346f2b\", \"ProjectionKey\":[{\"App\":\"Application\",\"Projection\":\"paa.wine_price\",\"WS\":1}]}" https://alpha2.dev.untill.ru/n10n/subscribe -H "Content-Type: application/json"
*/
func (s *httpService) subscribeHandler() http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		var parameters subscriberParamsType
		err := getJsonPayload(req, &parameters)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
		}
		logger.Info("n10n subscribe: ", parameters)
		for _, projection := range parameters.ProjectionKey {
			err = s.n10n.Subscribe(parameters.Channel, projection)
			if err != nil {
				log.Println(err)
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
}

/*
curl -G --data-urlencode "payload={\"Channel\": \"a23b2050-b90c-4ed1-adb7-1ecc4f346f2b\", \"ProjectionKey\":[{\"App\":\"Application\",\"Projection\":\"paa.wine_price\",\"WS\":1}]}" https://alpha2.dev.untill.ru/n10n/unsubscribe -H "Content-Type: application/json"
*/
func (s *httpService) unSubscribeHandler() http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		var parameters subscriberParamsType
		err := getJsonPayload(req, &parameters)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
		}
		logger.Info("n10n unsubscribe: ", parameters)
		for _, projection := range parameters.ProjectionKey {
			err = s.n10n.Unsubscribe(parameters.Channel, projection)
			if err != nil {
				log.Println(err)
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
}

// curl -X POST "http://localhost:3001/n10n/update" -H "Content-Type: application/json" -d "{\"App\":\"Application\",\"Projection\":\"paa.price\",\"WS\":1}"
// TODO: eliminate after airs-bp3 integration tests implementation
func (s *httpService) updateHandler() http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		var p in10n.ProjectionKey
		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			log.Println(err)
			http.Error(resp, "Error when read request body", http.StatusInternalServerError)
			return
		}
		err = json.Unmarshal(body, &p)
		if err != nil {
			log.Println(err)
			http.Error(resp, "Error when parse request body", http.StatusBadRequest)
			return
		}

		params := mux.Vars(req)
		offset := params["offset"]
		if off, err := strconv.ParseInt(offset, 10, 64); err == nil {
			s.n10n.Update(p, istructs.Offset(off))
		}
	}
}

func getJsonPayload(req *http.Request, payload *subscriberParamsType) (err error) {
	jsonParam, ok := req.URL.Query()["payload"]
	if !ok || len(jsonParam[0]) < 1 {
		log.Println("Url parameter with payload (channel id and projection key) is missing")
		return errors.New("url parameter with payload (channel id and projection key) is missing")
	}
	err = json.Unmarshal([]byte(jsonParam[0]), payload)
	if err != nil {
		log.Println(err)
		err = fmt.Errorf("cannot unmarshal input payload %w", err)
	}
	return err
}
