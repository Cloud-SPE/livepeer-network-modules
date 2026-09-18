# Assemble the actual module validators/key tools from published runtime images.
ARG REGISTRY=tztcloud
ARG TAG=v2.0.0
FROM ${REGISTRY}/livepeer-pool-controller:${TAG} AS controller
FROM ${REGISTRY}/livepeer-capability-broker:${TAG} AS broker
FROM ${REGISTRY}/livepeer-member-portal:${TAG} AS portal
FROM ${REGISTRY}/livepeer-pool-reconciler:${TAG} AS reconciler
FROM ${REGISTRY}/livepeer-pool-payout-executor:${TAG} AS executor
FROM ${REGISTRY}/livepeer-secure-orch-console:${TAG} AS signer
FROM python:3.12-slim
RUN apt-get update && apt-get install -y --no-install-recommends openssl skopeo ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=controller /usr/local/bin/livepeer-pool-controller /usr/local/bin/
COPY --from=controller /etc/livepeer/templates /etc/livepeer/templates
COPY --from=broker /usr/local/bin/livepeer-capability-broker /usr/local/bin/
COPY --from=portal /usr/local/bin/livepeer-member-portal /usr/local/bin/
COPY --from=reconciler /usr/local/bin/livepeer-pool-reconciler /usr/local/bin/
COPY --from=executor /usr/local/bin/livepeer-pool-payout-executor /usr/local/bin/
COPY --from=signer /usr/local/bin/secure-orch-keygen /usr/local/bin/
COPY pool-setup/setup.py pool-setup/render.py /app/
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
WORKDIR /app
ENTRYPOINT ["python3", "/app/setup.py"]
CMD ["plan"]
