# Optional model plane: the same interfaces as the Go provider, backed by
# SciPy/statsmodels. The engine falls back to Go automatically if this is down.
FROM python:3.12-slim

WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

ENV SEMEION_MODEL_PORT=8899 \
    SEMEION_MODEL_HOST=0.0.0.0
EXPOSE 8899
CMD ["python", "server.py"]
