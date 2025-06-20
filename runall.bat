@echo off
REM Define o título da janela principal
TITLE Painel de Controle do Sniper Bot

echo =======================================================
echo.
echo    INICIANDO O ECOSSISTEMA DO SNIPER BOT
echo.
echo =======================================================

REM Passo 1: Iniciar a infraestrutura Docker em segundo plano (-d)
echo [1/5] Iniciando infraestrutura Docker (Kafka, DB, etc.)...
docker-compose up -d

REM Verifica se o comando anterior foi bem sucedido
if %errorlevel% neq 0 (
    echo.
    echo !!! FALHA AO INICIAR O DOCKER. Verifique se o Docker Desktop esta rodando. !!!
    pause
    exit /b
)

echo.
echo [2/5] Aguardando 20 segundos para os servicos estabilizarem...
REM O timeout dá tempo para o Kafka e Zookeeper iniciarem completamente.
timeout /t 20 /nobreak > NUL

echo.
echo [3/5] Criando o topico 'mempool-transactions' no Kafka (se nao existir)...
docker exec kafka kafka-topics --create --topic mempool-transactions --bootstrap-server kafka:29092 --partitions 1 --replication-factor 1

echo.
echo [4/5] Iniciando o PRODUTOR (Coletor do Mempool) em uma nova janela...
REM O comando 'start' abre uma nova janela de terminal.
REM 'cmd /k' mantém a janela aberta mesmo se o programa fechar, para vermos os logs.
start "Produtor (Sniper Bot)" cmd /k "go run main.go"

echo.
echo [5/5] Iniciando o CONSUMIDOR (Arquivista de Dados) em uma nova janela...
REM Usamos o '/d' para especificar o diretório de trabalho do consumidor.
start "Consumidor (Data Consumer)" cmd /k "cd data-consumer && go run main.go"


echo.
echo =======================================================
echo.
echo    TODOS OS SISTEMAS FORAM INICIADOS!
echo.
echo    - Verifique as duas novas janelas de terminal.
echo    - Acesse a interface do Kafka em http://localhost:8080
echo    - Acesse o banco de dados no DBeaver na porta 5433
echo.
echo =======================================================

pause