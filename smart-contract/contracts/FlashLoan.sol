// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

// Interfaces necessárias para interagir com Aave e DEXs
interface IPool {
    function flashLoanSimple(address receiverAddress, address asset, uint256 amount, bytes calldata params, uint16 referralCode) external;
}

interface IUniswapV2Router02 {
    function swapExactTokensForTokens(
        uint amountIn,
        uint amountOutMin,
        address[] calldata path,
        address to,
        uint deadline
    ) external returns (uint[] memory amounts);
}

interface IERC20 {
    function approve(address spender, uint256 amount) external returns (bool);
    function transfer(address to, uint256 amount) external returns (bool);
    function balanceOf(address account) external view returns (uint256);
}

// Nosso contrato executor
contract FlashLoanExecutor {
    address public owner;
    IPool public immutable POOL;

    // Evento para facilitar o acompanhamento dos lucros
    event ProfitMade(address indexed token, uint256 amount);

    constructor(address _poolAddress) {
        owner = msg.sender;
        POOL = IPool(_poolAddress);
    }

    // Função que nosso bot em GO irá chamar
    function startArbitrage(
        address _tokenIn,      // O token do flash loan (ex: WETH)
        address _tokenOut,     // O token intermediário (ex: USDC)
        uint256 _amount,       // A quantidade do flash loan
        address _routerBuy,    // O roteador para comprar (ex: Sushiswap)
        address _routerSell    // O roteador para vender (ex: Uniswap)
    ) external {
        require(msg.sender == owner, "Only owner can start arbitrage");

        // Prepara os parâmetros para o flash loan
        address[] memory pathBuy = new address[](2);
        pathBuy[0] = _tokenIn;
        pathBuy[1] = _tokenOut;

        address[] memory pathSell = new address[](2);
        pathSell[0] = _tokenOut;
        pathSell[1] = _tokenIn;

        bytes memory params = abi.encode(pathBuy, pathSell, _routerBuy, _routerSell);

        // Pede o Flash Loan
        POOL.flashLoanSimple(address(this), _tokenIn, _amount, params, 0);
    }

    /**
     * @dev Função de callback do Aave - O CORAÇÃO DA OPERAÇÃO
     */
    function executeOperation(
        address asset,
        uint256 amount,
        uint256 premium,
        address initiator,
        bytes calldata params
    ) external returns (bool) {
        require(msg.sender == address(POOL), "Caller is not the Aave Pool");
        
        // Decodifica os caminhos e roteadores que enviamos
        (address[] memory pathBuy, address[] memory pathSell, address routerBuy, address routerSell) = abi.decode(params, (address[], address[], address, address));

        // 1. Aprova o primeiro roteador a gastar o token emprestado
        IERC20(asset).approve(routerBuy, amount);

        // 2. Executa o primeiro SWAP (Comprar token intermediário)
        IUniswapV2Router02(routerBuy).swapExactTokensForTokens(
            amount,
            0, // amountOutMin = 0, aceitamos qualquer valor por velocidade
            pathBuy,
            address(this),
            block.timestamp
        );

        // 3. Pega o saldo do token intermediário e aprova o segundo roteador
        uint256 intermediateTokenBalance = IERC20(pathBuy[1]).balanceOf(address(this));
        IERC20(pathBuy[1]).approve(routerSell, intermediateTokenBalance);

        // 4. Executa o segundo SWAP (Vender de volta para o token original)
        IUniswapV2Router02(routerSell).swapExactTokensForTokens(
            intermediateTokenBalance,
            0,
            pathSell,
            address(this),
            block.timestamp
        );

        // 5. Verifica o Lucro e Paga o Empréstimo
        uint256 finalBalance = IERC20(asset).balanceOf(address(this));
        uint256 amountToRepay = amount + premium;

        // Se o que temos no final não for suficiente para pagar, a transação falhará (o que é bom!)
        require(finalBalance >= amountToRepay, "Arbitrage failed: not enough funds to repay");

        // Paga o empréstimo + taxa para o Aave
        IERC20(asset).approve(address(POOL), amountToRepay);

        // 6. Transfere o Lucro para o Dono do Contrato
        uint256 profit = finalBalance - amountToRepay;
        if (profit > 0) {
            IERC20(asset).transfer(owner, profit);
            emit ProfitMade(asset, profit);
        }

        return true;
    }
}