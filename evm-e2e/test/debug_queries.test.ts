import { beforeAll, describe, expect, it } from "@jest/globals"
import { parseEther, TransactionReceipt } from "ethers"

import { TestERC20__factory } from "../types"
import { provider, TEST_TIMEOUT, TX_WAIT_TIMEOUT } from "./setup"
import { alice, deployContractTestERC20, hexify } from "./utils"

describe("debug queries", () => {
  let contractAddress: string
  let txHash: string
  let txIndex: number
  let blockNumber: number
  let blockHash: string

  beforeAll(async () => {
    // Deploy ERC-20 contract
    const contract = await deployContractTestERC20()
    contractAddress = await contract.getAddress()

    // Execute some contract TX
    const txResponse = await contract.transfer(alice, parseEther("0.01"))
    await txResponse.wait(1, TX_WAIT_TIMEOUT)

    const receipt: TransactionReceipt = await provider.getTransactionReceipt(
      txResponse.hash,
    )
    txHash = txResponse.hash
    txIndex = txResponse.index
    blockNumber = receipt.blockNumber
    blockHash = receipt.blockHash
  }, TEST_TIMEOUT)

  it("debug_traceBlockByNumber", async () => {
    const traceResult = await provider.send("debug_traceBlockByNumber", [
      blockNumber,
      {
        tracer: "callTracer",
        timeout: "3000s",
        tracerConfig: { onlyTopCall: false },
      },
    ])
    expectTrace(traceResult)
  })

  it("debug_traceBlockByHash", async () => {
    const traceResult = await provider.send("debug_traceBlockByHash", [
      blockHash,
      {
        tracer: "callTracer",
        timeout: "3000s",
        tracerConfig: { onlyTopCall: false },
      },
    ])
    expectTrace(traceResult)
  })

  it("debug_traceTransaction", async () => {
    const traceResult = await provider.send("debug_traceTransaction", [
      txHash,
      {
        tracer: "callTracer",
        timeout: "3000s",
        tracerConfig: { onlyTopCall: false },
      },
    ])
    expectTrace([{ result: traceResult }])
  })

  it("debug_traceCall", async () => {
    const contractInterface = TestERC20__factory.createInterface()
    const callData = contractInterface.encodeFunctionData("totalSupply")
    const tx = {
      to: contractAddress,
      data: callData,
      gas: hexify(1000_000),
    }
    const traceResult = await provider.send("debug_traceCall", [
      tx,
      "latest",
      {
        tracer: "callTracer",
        timeout: "3000s",
        tracerConfig: { onlyTopCall: false },
      },
    ])
    expectTrace([{ result: traceResult }])
  })

  // TODO: feat(evm-rpc): impl the debug_getBadBlocks EVM RPC method
  // ticket: https://github.com/NibiruChain/nibiru/issues/2279
  it("debug_getBadBlocks", async () => {
    try {
      const traceResult = await provider.send("debug_getBadBlocks", [])
      expect(traceResult).toBeDefined()
    } catch (err) {
      expect(err.message).toContain(
        "the method debug_getBadBlocks does not exist",
      )
    }
  })

  // TODO: feat(evm-rpc): impl the debug_storageRangeAt EVM RPC method
  // ticket: https://github.com/NibiruChain/nibiru/issues/2281
  it("debug_storageRangeAt", async () => {
    try {
      const traceResult = await provider.send("debug_storageRangeAt", [
        blockNumber,
        txIndex,
        contractAddress,
        "0x0",
        100,
      ])
      expect(traceResult).toBeDefined()
    } catch (err) {
      expect(err.message).toContain(
        "the method debug_storageRangeAt does not exist",
      )
    }
  })
})

const expectTrace = (traceResult: any[]) => {
  expect(traceResult).toBeDefined()
  expect(traceResult.length).toBeGreaterThan(0)

  const trace = traceResult[0]["result"]
  expect(trace).toHaveProperty("from")
  expect(trace).toHaveProperty("to")
  expect(trace).toHaveProperty("gas")
  expect(trace).toHaveProperty("gasUsed")
  expect(trace).toHaveProperty("input")
  expect(trace).toHaveProperty("output")
  expect(trace).toHaveProperty("type", "CALL")
}
