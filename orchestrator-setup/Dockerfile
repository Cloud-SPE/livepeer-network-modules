ARG BROKER_IMAGE=tztcloud/livepeer-capability-broker:v2.0.0
ARG KEYGEN_IMAGE=tztcloud/livepeer-secure-orch-console:v2.0.0
FROM ${BROKER_IMAGE} AS broker
FROM ${KEYGEN_IMAGE} AS keygen
FROM docker:cli AS dockercli
FROM python:3-slim
COPY --from=broker /usr/local/bin/livepeer-capability-broker /usr/local/bin/
COPY --from=keygen /usr/local/bin/secure-orch-keygen /usr/local/bin/
COPY --from=dockercli /usr/local/bin/docker /usr/local/bin/
COPY --from=dockercli /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose
COPY orchestrator-setup/setup.py /app/setup.py
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
WORKDIR /app
ENTRYPOINT ["python3", "/app/setup.py"]
CMD ["plan"]
