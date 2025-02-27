//(C) Copyright [2020] Hewlett Packard Enterprise Development LP
//
//Licensed under the Apache License, Version 2.0 (the "License"); you may
//not use this file except in compliance with the License. You may obtain
//a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
//WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
//License for the specific language governing permissions and limitations
// under the License.

//Package caphandler ...
package caphandler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ODIM-Project/ODIM/lib-dmtf/model"
	"github.com/ODIM-Project/ODIM/lib-utilities/response"
	"github.com/ODIM-Project/PluginCiscoACI/capmodel"
	"github.com/ODIM-Project/PluginCiscoACI/caputilities"
	"github.com/ODIM-Project/PluginCiscoACI/config"
	"github.com/ODIM-Project/PluginCiscoACI/db"
	iris "github.com/kataras/iris/v12"
	log "github.com/sirupsen/logrus"
)

// GetPortCollection fetches the ports  which are linked to that switch
func GetPortCollection(ctx iris.Context) {
	uri := ctx.Request().RequestURI
	switchID := ctx.Params().Get("switchID")

	// get all port which are store under that switch
	portData, err := capmodel.GetSwitchPort(switchID)
	if err != nil {
		errMsg := fmt.Sprintf("failed to fetch port data for uri %s: %s", uri, err.Error())
		createDbErrResp(ctx, err, errMsg, []interface{}{"Port", uri})
		return
	}

	var members = []*model.Link{}
	for i := 0; i < len(portData); i++ {
		members = append(members, &model.Link{
			Oid: uri + "/" + portData[i],
		})
	}

	portCollectionResponse := model.Collection{
		ODataContext: "/ODIM/v1/$metadata#PortCollection.PortCollection",
		ODataID:      uri,
		ODataType:    "#PortCollection.PortCollection",
		Description:  "PortCollection view",
		Name:         "Ports",
		Members:      members,
		MembersCount: len(members),
	}
	ctx.StatusCode(http.StatusOK)
	ctx.JSON(portCollectionResponse)
}

// GetPortInfo fetches the port info for given port id
func GetPortInfo(ctx iris.Context) {
	uri := ctx.Request().RequestURI
	switchID := ctx.Params().Get("switchID")
	fabricID := ctx.Params().Get("id")
	fabricData, err := capmodel.GetFabric(fabricID)
	if err != nil {
		errMsg := fmt.Sprintf("failed to fetch port data for uri %s: %s", uri, err.Error())
		createDbErrResp(ctx, err, errMsg, []interface{}{"Fabric", fabricID})
		return
	}
	portData := getPortData(ctx, uri)
	if portData == nil {
		return
	}
	getPortAddtionalAttributes(fabricData.PodID, switchID, portData)
	ctx.StatusCode(http.StatusOK)
	ctx.JSON(portData)

}

// PatchPort Update the given port with provied information
func PatchPort(ctx iris.Context) {
	uri := ctx.Request().RequestURI
	var port model.Port
	err := ctx.ReadJSON(&port)
	if err != nil {
		errorMessage := "error while trying to get JSON body from the  request: " + err.Error()
		log.Error(errorMessage)
		resp := updateErrorResponse(response.MalformedJSON, errorMessage, nil)
		ctx.StatusCode(http.StatusBadRequest)
		ctx.JSON(resp)
		return
	}
	portData := getPortData(ctx, uri)
	if portData == nil {
		return
	}
	checkFlag := false

	if port.Links != nil {
		if port.Links.ConnectedPorts != nil {
			if len(port.Links.ConnectedPorts) > 0 {
				//Assuming we have only one connected port
				ethernetURI := port.Links.ConnectedPorts[0].Oid
				//Check on ODIM if ethernet is valid
				reqURL := config.Data.ODIMConf.URL + ethernetURI
				odimUsername := config.Data.ODIMConf.UserName
				odimPassword := config.Data.ODIMConf.Password
				for key, value := range config.Data.URLTranslation.SouthBoundURL {
					reqURL = strings.Replace(reqURL, key, value, -1)
				}
				enigma, err := caputilities.NewEnigma(string(config.Data.KeyCertConf.RSAPrivateKeyPath))
				if err != nil {
					errMsg := fmt.Sprintf("Error while trying to read private key path %s ", err.Error())
					log.Error(errMsg)
					resp := updateErrorResponse(response.InternalError, errMsg, nil)
					ctx.StatusCode(http.StatusServiceUnavailable)
					ctx.JSON(resp)
					return
				}
				//decrypting odim password
				odimPwd := string(enigma.Decrypt(odimPassword))
				checkFlag, err = caputilities.CheckValidityOfEthernet(reqURL, odimUsername, odimPwd)
				if err != nil {
					errMsg := fmt.Sprintf("Error while trying to contact ODIM")
					log.Error(errMsg)
					resp := updateErrorResponse(response.InternalError, errMsg, nil)
					ctx.StatusCode(http.StatusServiceUnavailable)
					ctx.JSON(resp)
					return
				}
				if !checkFlag {
					errMsg := fmt.Sprintf("Ethernet data for uri %s not found", reqURL)
					log.Error(errMsg)
					resp := updateErrorResponse(response.ResourceNotFound, errMsg, []interface{}{"Ethernet", reqURL})
					ctx.StatusCode(http.StatusNotFound)
					ctx.JSON(resp)
					return
				}
				portData.Links = &model.PortLinks{}
				portData.Links.ConnectedPorts = []model.Link{}
				portData.Links.ConnectedPorts = append(portData.Links.ConnectedPorts, model.Link{Oid: ethernetURI})
			} else {
				portData.Links.ConnectedPorts = nil
			}
		} else {
			portData.Links.ConnectedPorts = nil
		}
	}
	if err := capmodel.UpdatePort(uri, portData); err != nil {
		errMsg := fmt.Sprintf("failed to update port data for uri %s: %s", uri, err.Error())
		createDbErrResp(ctx, err, errMsg, []interface{}{"Ports", uri})
		return
	}
	ctx.StatusCode(http.StatusOK)
	ctx.JSON(portData)
}

func getPortAddtionalAttributes(fabricID, switchID string, p *model.Port) {
	switchIDData := strings.Split(switchID, ":")
	PortInfoResponse, err := caputilities.GetPortInfo(fabricID, switchIDData[1], p.PortID)
	if err != nil {
		log.Error("Unable to get addtional port info " + err.Error())
		return
	}
	portInfoData := PortInfoResponse.IMData[0].PhysicalInterface.Attributes
	operationState := portInfoData["operSt"].(string)
	if operationState == "up" {
		p.LinkState = "Enabled"
		p.LinkStatus = "LinkUp"
		p.InterfaceEnabled = true
	} else {
		p.LinkState = "Disabled"
		p.LinkStatus = "LinkDown"
		p.InterfaceEnabled = false
	}
	curSpeedData := strings.Split(portInfoData["operSpeed"].(string), "G")
	data, err := strconv.ParseFloat(curSpeedData[0], 64)
	if err != nil {
		log.Error("Unable to get current speed  of port " + err.Error())
	}
	p.CurrentSpeedGbps = data
	portsHealthResposne, err := caputilities.GetPortHealth(fabricID, switchIDData[1], p.PortID)
	if err != nil {
		log.Error("Unable to get Health of port " + err.Error())
		return
	}

	Healthdata := portsHealthResposne.IMData[0].HealthData.Attributes
	currentHealthValue := Healthdata["cur"].(string)
	healthValue, err := strconv.Atoi(currentHealthValue)
	if err != nil {
		log.Error("Unable to convert current Health value:" + currentHealthValue + " go the error" + err.Error())
		return
	}
	var portStatus = model.Status{
		State: p.LinkState,
	}
	if healthValue > 90 {
		portStatus.Health = "OK"
	} else if healthValue <= 90 && healthValue < 30 {
		portStatus.Health = "Warning"
	} else {
		portStatus.Health = "Critical"
	}

	p.Status = &portStatus
	return
}

func updateErrorResponse(statusMsg, errMsg string, msgArgs []interface{}) interface{} {
	args := response.Args{
		Code:    response.GeneralError,
		Message: "",
		ErrorArgs: []response.ErrArgs{
			response.ErrArgs{
				StatusMessage: statusMsg,
				ErrorMessage:  errMsg,
				MessageArgs:   msgArgs,
			},
		},
	}
	return args.CreateGenericErrorResponse()
}

func createDbErrResp(ctx iris.Context, err error, errMsg string, msgArgs []interface{}) (int, interface{}) {
	var resp interface{}
	var statusCode int
	switch {
	case errors.Is(err, db.ErrorKeyNotFound):
		resp = updateErrorResponse(response.ResourceNotFound, errMsg, msgArgs)
		statusCode = http.StatusNotFound
	case errors.Is(err, db.ErrorServiceUnavailable):
		resp = updateErrorResponse(response.CouldNotEstablishConnection, errMsg, nil)
		statusCode = http.StatusServiceUnavailable
	case errors.Is(err, db.ErrorKeyAlreadyExist):
		resp = updateErrorResponse(response.ResourceAlreadyExists, errMsg, msgArgs)
		statusCode = http.StatusConflict
	default:
		resp = updateErrorResponse(response.InternalError, errMsg, nil)
		statusCode = http.StatusInternalServerError
	}
	log.Error(errMsg)
	if ctx != nil {
		ctx.StatusCode(statusCode)
		ctx.JSON(resp)
	}
	return statusCode, resp
}

func getPortData(ctx iris.Context, portOID string) *model.Port {
	log.Info("Port uri" + portOID)
	portData, err := capmodel.GetPort(portOID)
	if err != nil {
		errMsg := fmt.Sprintf("failed to fetch port data for uri %s: %s", portOID, err.Error())
		createDbErrResp(ctx, err, errMsg, []interface{}{"Ports", portOID})
		return nil
	}
	return portData
}
