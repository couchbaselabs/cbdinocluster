package cloudinstancecontrol

import (
	"context"
	"fmt"
	"time"

	"github.com/couchbaselabs/cbdinocluster/utils/awscontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/azurecontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/gcpcontrol"
	"go.uber.org/zap"
)

type SelfIdentifyController struct {
	Logger *zap.Logger
}

func (c *SelfIdentifyController) Identify(ctx context.Context) (interface{}, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)

	awsWaitCh := make(chan struct{})
	var awsRes *awscontrol.LocalInstanceInfo
	var awsErr error
	go func() {
		liCtrl := awscontrol.LocalInstanceController{
			Logger: c.Logger,
		}

		localInstance, err := liCtrl.Identify(timeoutCtx)
		if err != nil {
			awsErr = err
			awsWaitCh <- struct{}{}
			return
		}

		awsRes = localInstance
		cancel()
		awsWaitCh <- struct{}{}
	}()

	gcpWaitCh := make(chan struct{})
	var gcpRes *gcpcontrol.LocalInstanceInfo
	var gcpErr error
	go func() {
		liCtrl := gcpcontrol.LocalInstanceController{
			Logger: c.Logger,
		}

		localInstanceGcp, err := liCtrl.Identify(timeoutCtx)
		if err != nil {
			gcpErr = err
			gcpWaitCh <- struct{}{}
			return
		}

		gcpRes = localInstanceGcp
		cancel()
		gcpWaitCh <- struct{}{}
	}()

	azureWaitCh := make(chan struct{})
	var azureRes *azurecontrol.LocalVmInfo
	var azureErr error
	go func() {
		lvmCtrl := azurecontrol.LocalVmController{
			Logger: c.Logger,
		}

		localVm, err := lvmCtrl.Identify(timeoutCtx)
		if err != nil {
			azureErr = err
			azureWaitCh <- struct{}{}
			return
		}

		azureRes = localVm
		cancel()
		azureWaitCh <- struct{}{}
	}()

	<-awsWaitCh
	<-azureWaitCh
	<-gcpWaitCh

	if awsRes != nil {
		return awsRes, nil
	} else if azureRes != nil {
		return azureRes, nil
	} else if gcpRes != nil {
		return gcpRes, nil
	}

	return nil, fmt.Errorf("failed to identify local instance (aws: %s, azure: %s, gcp: %s)", awsErr, azureErr, gcpErr)
}
