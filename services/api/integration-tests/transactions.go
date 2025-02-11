// +build integration

package integrationtests

import (
	"context"
	"github.com/consensys/orchestrate/pkg/types/api"
	"testing"
	"time"

	"github.com/consensys/orchestrate/pkg/errors"
	"github.com/consensys/orchestrate/pkg/sdk/client"
	clientutils "github.com/consensys/orchestrate/pkg/toolkit/app/http/client-utils"
	"github.com/consensys/orchestrate/pkg/types/entities"
	"github.com/consensys/orchestrate/pkg/types/testutils"
	"github.com/consensys/orchestrate/pkg/types/tx"
	"github.com/consensys/orchestrate/pkg/utils"
	"github.com/consensys/orchestrate/services/api/service/controllers"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	waitForEnvelopeTimeOut = 3 * time.Second
)

// transactionsTestSuite is a test suite for Transaction Scheduler Transactions controller
type transactionsTestSuite struct {
	suite.Suite
	client   client.OrchestrateClient
	contract *api.ContractResponse
	env      *IntegrationEnvironment
}

func (s *transactionsTestSuite) SetupSuite() {
	// The registered contract for this test suite is an ERC-20 contract
	contract, err := s.client.RegisterContract(s.env.ctx, testutils.FakeRegisterContractRequest())
	require.NoError(s.T(), err)

	s.contract = contract
}

func (s *transactionsTestSuite) TestValidation() {
	ctx := s.env.ctx

	s.T().Run("should fail if payload is invalid", func(t *testing.T) {
		txRequest := testutils.FakeSendTransactionRequest()
		txRequest.ChainName = ""

		_, err := s.client.SendContractTransaction(ctx, txRequest)

		assert.True(t, errors.IsInvalidFormatError(err))
	})

	s.T().Run("should fail with 400 if from account is set and oneTimeKeyEnabled", func(t *testing.T) {
		txRequest := testutils.FakeSendTransactionRequest()
		txRequest.Params.OneTimeKey = true

		_, err := s.client.SendContractTransaction(ctx, txRequest)

		assert.True(t, errors.IsInvalidFormatError(err))
	})

	s.T().Run("should fail if idempotency key is identical but different params", func(t *testing.T) {
		txRequest := testutils.FakeSendTransactionRequest()
		txRequest.Params.ContractTag = s.contract.Tag
		txRequest.Params.ContractName = s.contract.Name

		rctx := context.WithValue(ctx, clientutils.RequestHeaderKey, map[string]string{
			controllers.IdempotencyKeyHeader: utils.RandString(16),
		})

		_, err := s.client.SendContractTransaction(rctx, txRequest)
		assert.NoError(t, err)

		txRequest.Params.MethodSignature = "decreaseAllowance(address,uint256)"
		txRequest.Params.Args = []interface{}{"0x905B88EFf8Bda1543d4d6f4aA05afef143D27E18", 1}
		_, err = s.client.SendContractTransaction(rctx, txRequest)
		assert.True(t, errors.IsConstraintViolatedError(err))
	})

	s.T().Run("should fail with 422 if chains cannot be fetched", func(t *testing.T) {
		txRequest := testutils.FakeSendTransactionRequest()
		txRequest.ChainName = "inexistentChain"

		_, err := s.client.SendContractTransaction(ctx, txRequest)

		assert.True(t, errors.IsInvalidParameterError(err))
	})

	s.T().Run("should fail with 422 if account does not exist", func(t *testing.T) {
		// Create a txRequest with an inexisting account
		txRequest := testutils.FakeSendTransactionRequest()
		from := ethcommon.HexToAddress("0x905B88EFf8Bda1543d4d6f4aA05afef143D27E18")
		txRequest.Params.From = &from

		_, err := s.client.SendContractTransaction(ctx, txRequest)

		assert.True(t, errors.IsInvalidParameterError(err))
	})
}

func (s *transactionsTestSuite) TestSuccess() {
	ctx := s.env.ctx

	s.T().Run("should send a contract transaction successfully", func(t *testing.T) {
		txRequest := testutils.FakeSendTransactionRequest()

		txRequest.Params.From = nil
		txRequest.Params.OneTimeKey = true
		txRequest.Params.ContractTag = s.contract.Tag
		txRequest.Params.ContractName = s.contract.Name

		IdempotencyKey := utils.RandString(16)
		rctx := context.WithValue(ctx, clientutils.RequestHeaderKey, map[string]string{
			controllers.IdempotencyKeyHeader: IdempotencyKey,
		})
		txResponse, err := s.client.SendContractTransaction(rctx, txRequest)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}
		assert.NotEmpty(t, txResponse.UUID)
		assert.NotEmpty(t, txResponse.IdempotencyKey)

		txResponseGET, err := s.client.GetTxRequest(ctx, txResponse.UUID)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}

		job := txResponseGET.Jobs[0]

		assert.NotEmpty(t, txResponseGET.UUID)
		assert.NotEmpty(t, job.UUID)
		assert.Equal(t, entities.StatusStarted, job.Status)
		assert.Empty(t, job.Transaction.From)
		assert.Equal(t, txRequest.Params.To.Hex(), job.Transaction.To.Hex())
		assert.Equal(t, entities.EthereumTransaction, job.Type)

		evlp, err := s.env.consumer.WaitForEnvelope(job.ScheduleUUID, s.env.kafkaTopicConfig.Sender, waitForEnvelopeTimeOut)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}
		assert.Equal(t, job.ScheduleUUID, evlp.GetID())
		assert.Equal(t, job.UUID, evlp.GetJobUUID())
		assert.True(t, evlp.IsOneTimeKeySignature())
		assert.Equal(t, tx.JobTypeMap[entities.EthereumTransaction].String(), evlp.GetJobTypeString())
		assert.Equal(t, evlp.PartitionKey(), "")
	})

	s.T().Run("should send a tessera transaction successfully", func(t *testing.T) {
		txRequest := testutils.FakeSendTesseraRequest()
		txRequest.Params.ContractTag = s.contract.Tag
		txRequest.Params.ContractName = s.contract.Name

		IdempotencyKey := utils.RandString(16)
		rctx := context.WithValue(ctx, clientutils.RequestHeaderKey, map[string]string{
			controllers.IdempotencyKeyHeader: IdempotencyKey,
		})
		txResponse, err := s.client.SendContractTransaction(rctx, txRequest)
		require.NoError(t, err)
		assert.NotEmpty(t, txResponse.UUID)
		assert.NotEmpty(t, txResponse.IdempotencyKey)

		txResponseGET, err := s.client.GetTxRequest(ctx, txResponse.UUID)
		require.NoError(t, err)
		assert.NotEmpty(t, txResponseGET.UUID)
		assert.Len(t, txResponseGET.Jobs, 2)

		privTxJob := txResponseGET.Jobs[0]
		assert.NotEmpty(t, privTxJob.UUID)
		assert.Equal(t, entities.StatusStarted, privTxJob.Status)
		assert.Equal(t, txRequest.Params.From.Hex(), privTxJob.Transaction.From.Hex())
		assert.Equal(t, txRequest.Params.To.Hex(), privTxJob.Transaction.To.Hex())
		assert.Equal(t, entities.TesseraPrivateTransaction, privTxJob.Type)

		markingTxJob := txResponseGET.Jobs[1]
		assert.NotEmpty(t, markingTxJob.UUID)
		assert.Equal(t, entities.StatusCreated, markingTxJob.Status)
		assert.Equal(t, entities.TesseraMarkingTransaction, markingTxJob.Type)

		privTxEvlp, err := s.env.consumer.WaitForEnvelope(privTxJob.ScheduleUUID, s.env.kafkaTopicConfig.Sender, waitForEnvelopeTimeOut)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}

		assert.Equal(t, privTxJob.ScheduleUUID, privTxEvlp.GetID())
		assert.Equal(t, privTxJob.UUID, privTxEvlp.GetJobUUID())
		assert.False(t, privTxEvlp.IsOneTimeKeySignature())
		assert.Equal(t, tx.JobTypeMap[entities.TesseraPrivateTransaction].String(), privTxEvlp.GetJobTypeString())
	})

	s.T().Run("should send an EEA transaction successfully", func(t *testing.T) {
		txRequest := testutils.FakeSendEEARequest()
		txRequest.Params.ContractTag = s.contract.Tag
		txRequest.Params.ContractName = s.contract.Name

		txResponse, err := s.client.SendContractTransaction(ctx, txRequest)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}
		assert.NotEmpty(t, txResponse.UUID)

		txResponseGET, err := s.client.GetTxRequest(ctx, txResponse.UUID)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}

		assert.NotEmpty(t, txResponseGET.UUID)
		assert.Len(t, txResponseGET.Jobs, 2)

		privTxJob := txResponseGET.Jobs[0]
		assert.NotEmpty(t, privTxJob.UUID)
		assert.Equal(t, entities.StatusStarted, privTxJob.Status)
		assert.Equal(t, txRequest.Params.From.Hex(), privTxJob.Transaction.From.Hex())
		assert.Equal(t, txRequest.Params.To.Hex(), privTxJob.Transaction.To.Hex())
		assert.Equal(t, entities.EEAPrivateTransaction, privTxJob.Type)

		markingTxJob := txResponseGET.Jobs[1]
		assert.NotEmpty(t, markingTxJob.UUID)
		assert.Equal(t, entities.StatusCreated, markingTxJob.Status)
		assert.Equal(t, entities.EEAMarkingTransaction, markingTxJob.Type)

		privTxEvlp, err := s.env.consumer.WaitForEnvelope(privTxJob.ScheduleUUID, s.env.kafkaTopicConfig.Sender, waitForEnvelopeTimeOut)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}

		assert.Equal(t, privTxJob.ScheduleUUID, privTxEvlp.GetID())
		assert.Equal(t, privTxJob.UUID, privTxEvlp.GetJobUUID())
		assert.Equal(t, tx.JobTypeMap[entities.EEAPrivateTransaction].String(), privTxEvlp.GetJobTypeString())
	})

	s.T().Run("should send a deploy contract transaction successfully", func(t *testing.T) {
		contractReq := testutils.FakeRegisterContractRequest()
		_, err := s.client.RegisterContract(ctx, contractReq)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}

		txRequest := testutils.FakeDeployContractRequest()
		txRequest.Params.ContractName = contractReq.Name
		txResponse, err := s.client.SendDeployTransaction(ctx, txRequest)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}
		assert.NotEmpty(t, txResponse.UUID)

		txResponseGET, err := s.client.GetTxRequest(ctx, txResponse.UUID)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}

		job := txResponseGET.Jobs[0]

		assert.NotEmpty(t, txResponseGET.UUID)
		assert.NotEmpty(t, job.UUID)
		assert.Equal(t, entities.StatusStarted, job.Status)
		assert.Equal(t, txRequest.Params.From.Hex(), job.Transaction.From.Hex())
		assert.Equal(t, entities.EthereumTransaction, job.Type)

		evlp, err := s.env.consumer.WaitForEnvelope(job.ScheduleUUID, s.env.kafkaTopicConfig.Sender, waitForEnvelopeTimeOut)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}
		assert.Equal(t, job.ScheduleUUID, evlp.GetID())
		assert.Equal(t, job.UUID, evlp.GetJobUUID())
		assert.Equal(t, tx.JobTypeMap[entities.EthereumTransaction].String(), evlp.GetJobTypeString())
	})

	s.T().Run("should send a raw transaction successfully", func(t *testing.T) {
		txRequest := testutils.FakeSendRawTransactionRequest()
		IdempotencyKey := utils.RandString(16)
		rctx := context.WithValue(ctx, clientutils.RequestHeaderKey, map[string]string{
			controllers.IdempotencyKeyHeader: IdempotencyKey,
		})
		txResponse, err := s.client.SendRawTransaction(rctx, txRequest)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}
		assert.NotEmpty(t, txResponse.UUID)
		assert.NotEmpty(t, txResponse.IdempotencyKey)

		txResponseGET, err := s.client.GetTxRequest(ctx, txResponse.UUID)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}

		job := txResponseGET.Jobs[0]

		assert.NotEmpty(t, txResponseGET.UUID)
		assert.NotEmpty(t, job.UUID)
		assert.Equal(t, entities.StatusStarted, job.Status)
		assert.Equal(t, txRequest.Params.Raw, job.Transaction.Raw)
		assert.Equal(t, entities.EthereumRawTransaction, job.Type)

		evlp, err := s.env.consumer.WaitForEnvelope(job.ScheduleUUID, s.env.kafkaTopicConfig.Sender, waitForEnvelopeTimeOut)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}
		assert.Equal(t, job.ScheduleUUID, evlp.GetID())
		assert.Equal(t, job.UUID, evlp.GetJobUUID())
		assert.Equal(t, tx.JobTypeMap[entities.EthereumRawTransaction].String(), evlp.GetJobTypeString())
	})

	s.T().Run("should send a transfer transaction successfully", func(t *testing.T) {
		txRequest := testutils.FakeSendTransferTransactionRequest()

		txResponse, err := s.client.SendTransferTransaction(ctx, txRequest)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}

		assert.NotEmpty(t, txResponse.UUID)

		txResponseGET, err := s.client.GetTxRequest(ctx, txResponse.UUID)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}

		job := txResponseGET.Jobs[0]

		assert.NotEmpty(t, txResponseGET.UUID)
		assert.NotEmpty(t, job.UUID)
		assert.Equal(t, entities.StatusStarted, job.Status)
		assert.Equal(t, txRequest.Params.Value.String(), job.Transaction.Value.String())
		assert.Equal(t, txRequest.Params.To.Hex(), job.Transaction.To.Hex())
		assert.Equal(t, txRequest.Params.From.Hex(), job.Transaction.From.Hex())
		assert.Equal(t, entities.EthereumTransaction, job.Type)

		evlp, err := s.env.consumer.WaitForEnvelope(job.ScheduleUUID, s.env.kafkaTopicConfig.Sender, waitForEnvelopeTimeOut)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}

		assert.Equal(t, job.ScheduleUUID, evlp.GetID())
		assert.Equal(t, job.UUID, evlp.GetJobUUID())
		assert.Equal(t, tx.JobTypeMap[entities.EthereumTransaction].String(), evlp.GetJobTypeString())
	})

	s.T().Run("should succeed if payloads and idempotency key are the same and return same schedule", func(t *testing.T) {
		txRequest := testutils.FakeSendTransactionRequest()
		txRequest.Params.ContractTag = s.contract.Tag
		txRequest.Params.ContractName = s.contract.Name

		idempotencyKey := utils.RandString(16)
		rctx := context.WithValue(ctx, clientutils.RequestHeaderKey, map[string]string{
			controllers.IdempotencyKeyHeader: idempotencyKey,
		})

		// Kill Kafka on first call so data is added in DB and status is CREATED but does not get update it and fetch previous one
		err := s.env.client.Stop(rctx, kafkaContainerID)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}

		_, err = s.client.SendContractTransaction(rctx, txRequest)
		assert.Error(t, err)

		err = s.env.client.StartServiceAndWait(rctx, kafkaContainerID, 10*time.Second)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}

		txResponse, err := s.client.SendContractTransaction(rctx, txRequest)
		if err != nil {
			assert.Fail(t, err.Error())
			return
		}
		job := txResponse.Jobs[0]
		assert.Equal(t, idempotencyKey, txResponse.IdempotencyKey)
		assert.Equal(t, entities.StatusFailed, job.Status)
	})
}
