const hre = require("hardhat");

async function main() {
  const aavePoolAddress = "0x7b0d91c1c221542642591a2bf354911a3ef1b74a";
  console.log("Fazendo deploy do contrato FlashLoanExecutor...");

  const executor = await hre.ethers.deployContract("FlashLoanExecutor", [aavePoolAddress]); // NOME ATUALIZADO
  await executor.waitForDeployment();

  const contractAddress = await executor.getAddress();
  console.log(`Contrato FlashLoanExecutor publicado com sucesso no endereço: ${contractAddress}`);
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});