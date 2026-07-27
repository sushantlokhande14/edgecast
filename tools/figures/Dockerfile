FROM python:3.12-slim
RUN pip install --no-cache-dir matplotlib==3.9.2
WORKDIR /w
COPY make_figures.py .
ENTRYPOINT ["python", "make_figures.py"]
