const path = require("path");
require("dotenv").config({ path: path.resolve(__dirname, ".env") });
require("@nomicfoundation/hardhat-toolbox");

// Agora lemos as duas URLs e a chave
const { ALCHEMY_HTTPS_URL, BOT_PRIVATE_KEY } = process.env;

if (!ALCHEMY_HTTPS_URL) {
  throw new Error("A variável ALCHEMY_HTTPS_URL não foi encontrada no seu .env");
}
if (!BOT_PRIVATE_KEY) {
  throw new Error("A variável BOT_PRIVATE_KEY não foi encontrada no seu .env");
}

/** @type import('hardhat/config').HardhatUserConfig */
module.exports = {
  solidity: "0.8.20",
  networks: {
    sepolia: {
      // Usamos a URL HTTPS aqui
      url: ALCHEMY_HTTPS_URL,
      accounts: [BOT_PRIVATE_KEY],
    },
  },
};