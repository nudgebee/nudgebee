import gc
import logging
import os
import tempfile
import uuid
from datetime import timedelta
from uuid import UUID

import numpy
import numpy as np
import pandas as pd
import tensorflow as tf
from server.model.models import Models
from server.model_store.awss3_store import AWSS3Store
from server.model_store.file_store import FileStore
from server.predictions_store.prediction_db_store import DatabasePredictionStore
from server.resource_models.k8s_resources import K8sResource
from server.utils.utils import (
    DatabaseEngine,
    DBConfig,
    FileConfig,
    ModelConfig,
    get_model_name,
    get_model_path,
    get_prediction_initial_set,
    get_prediction_run_batch_size,
    get_prediction_step,
    get_trace,
)
from sklearn.preprocessing import MinMaxScaler
from sqlalchemy import text
from tensorflow.keras.layers import LSTM, Dense
from tensorflow.keras.models import Sequential, load_model
from server.exception import CancelPrediction

logger = logging.getLogger(__name__)


class Model:
    def __init__(self, account_id: str, tenant_id: str, namespace: str, deployment_name: str, model_type: str):
        self.account_id = account_id
        self.tenant_id = tenant_id
        self.namespace = namespace
        self.deployment_name = deployment_name
        self.model_type = model_type
        self.path = get_model_path(
            account_id=account_id,
            tenant_id=tenant_id,
            namespace=namespace,
            deployment_name=deployment_name,
        )
        self.model_name = get_model_name(
            account_id=self.account_id,
            tenant_id=self.tenant_id,
            deployment_name=self.deployment_name,
            namespace=self.namespace,
            model_type=self.model_type,
        )

    def get_model_path(self):
        return os.path.join(self.path, self.model_name)

    def get_temp_file_path(self):
        destination = tempfile.gettempdir()
        os.makedirs(destination) if not os.path.exists(destination) else None
        return os.path.join(destination, self.model_name)

    def get_stored_model(self):
        destination = self.get_temp_file_path()
        if ModelConfig.model_store == "file":
            file_model_store = FileStore(FileConfig.base_path)
            status = file_model_store.fetch_model(model_name=self.get_model_path(), destination=destination)
        else:
            s3_model_store = AWSS3Store(ModelConfig.bucket_name)
            status = s3_model_store.fetch_model(model_name=self.get_model_path(), destination=destination)

        if status:
            model = load_model(destination)
            logger.info("Loaded model from file")
            model.summary()
            return model
        return None

    def is_model_present(self) -> bool:
        if ModelConfig.model_store == "file":
            return os.path.exists(self.get_model_path())
        else:
            s3_model_store = AWSS3Store(ModelConfig.bucket_name)
            return s3_model_store.is_present(path=self.get_model_path())

    @property
    def seq_length(self):
        return get_prediction_initial_set(account_id=self.account_id)

    def replica_model(self, train_x):
        with get_trace(__name__).start_as_current_span("get_model"):
            try:
                model = self.get_stored_model()
                if model:
                    return model
                model = Sequential()
                model.add(LSTM(50, return_sequences=True, input_shape=(self.seq_length, train_x.shape[2])))
                model.add(LSTM(50))
                model.add(Dense(1))
                model.compile(optimizer="adam", loss="mse")
                logger.info("Created model from scratch")
                model.summary()
                return model
            except Exception as e:
                msg = f"Failed to create model {e}"
                logger.error(msg)
                raise ValueError(msg)

    def feature_model(self, train_x):
        with get_trace(__name__).start_as_current_span("get_model"):
            try:
                model = self.get_stored_model()
                if model:
                    return model
                model = Sequential()
                model.add(LSTM(64, return_sequences=True, input_shape=(train_x.shape[1], train_x.shape[2])))
                model.add(LSTM(32))
                model.add(Dense(1))
                model.compile(optimizer="adam", loss="mse")
                model.summary()
                return model
            except Exception as e:
                msg = f"Failed to create model {e}"
                logger.error(msg)
                raise ValueError(msg)

    def get_model(self, train_x):
        if self.model_type.lower() == "replica":
            return self.replica_model(train_x)
        elif self.model_type.lower() in ["rps", "latency", "memory", "cpu"]:
            return self.feature_model(train_x)
        else:
            raise ValueError(f"Model type {self.model_type} not supported")

    def model_save(self, model):
        filepath = self.get_temp_file_path()
        model.save(filepath)
        return filepath

    def store_model(self, model):
        destination = self.model_save(model)
        with get_trace(__name__).start_as_current_span("store_model"):
            if ModelConfig.model_store == "file":
                file_model_store = FileStore("ml_files")
                logger.info(f"Saving the model to model store: {file_model_store.__class__}")
                file_model_store.save_model(self.get_model_path(), destination)
            else:
                s3_model_store = AWSS3Store(ModelConfig.bucket_name)
                logger.info(f"Saving the model to model store: {s3_model_store.__class__}")
                s3_model_store.save_model(self.get_model_path(), destination)

    def store_predictions(self, model_name: str, data: pd.DataFrame):
        with get_trace(__name__).start_as_current_span("store_predictions"):
            prediction_store = DatabasePredictionStore(url=DBConfig.url, table=DBConfig.table_name)
            prediction_store.store_predictions(model_name, data)


class HPAModel(Models):
    def __init__(self, tenant: str, account: str, namespace: str, deployment: str, resource_id: str):
        self.tenant = tenant
        self.account = account
        self.deployment = deployment
        self.namespace = namespace
        self.resource_id = resource_id

    def get_predictions(self, data: pd.DataFrame):
        with get_trace(__name__).start_as_current_span("get_predictions"):
            logger.info(f"Running predictions for resource id : {self.resource_id}")
            return self.run_model(data=data)

    def create_sequences(self, data, seq_length):
        xs, ys = [], []
        for i in range(len(data) - seq_length):
            x = data[i : i + seq_length]
            y = data[i + seq_length]
            xs.append(x)
            ys.append(y)
        return np.array(xs), np.array(ys)

    def run_model(self, data: pd.DataFrame) -> pd.DataFrame:
        with get_trace(__name__).start_as_current_span("run_model"):
            # data.to_csv(path_or_buf="input_data.csv")
            future_data = self.get_future_features(data=data)
            # future_data.to_csv(path_or_buf="intermediate_data.csv")
            predicted_replicas = self.predict_replicas(future_data=future_data, data=data)
            predicted_replicas = self.rule_based_model(data=predicted_replicas)
            # predicted_replicas[["inference_time", "cpu", "memory",
            #  "rps", "latency", "replicas"]].to_csv("final_output.csv")
            return predicted_replicas

    def is_model_present(self, feature="cpu") -> bool:
        model = Model(
            account_id=self.account,
            tenant_id=self.tenant,
            namespace=self.namespace,
            deployment_name=self.deployment,
            model_type=feature,
        )
        return model.is_model_present()

    def get_future_features(self, data: pd.DataFrame) -> pd.DataFrame:
        try:
            data = data.dropna()
            scalars, sequences = {}, {}
            epoch = int(ModelConfig.epoch)
            batch_size = get_prediction_run_batch_size(self.account)
            validation_split = float(ModelConfig.validation_split)
            seq_length = get_prediction_initial_set(self.account)
            future_steps = get_prediction_step(self.account)
            features = ["rps", "latency", "memory", "cpu"]
            for feature in features:
                scaler = MinMaxScaler()
                scaled_data = scaler.fit_transform(data[[feature]])
                scalars[feature] = scaler
                X, y = self.create_sequences(scaled_data, seq_length)
                sequences[feature] = (X, y)

            # Train a model for each feature
            models = {}
            for feature in features:
                logger.info(f"Training model for {feature}")
                x_train, y_train = sequences[feature]
                # Validate training data
                if len(x_train) == 0 or len(y_train) == 0:
                    msg = f"""Insufficient data to train model for feature
                     '{feature}'. Need at least {seq_length + 1}
                     data points, but got {len(data)}."""
                    logger.warning(msg)
                    raise CancelPrediction

                # Adjust validation_split based on available data
                # Need at least 5 samples to use validation_split
                min_samples_for_validation = 5
                effective_validation_split = validation_split
                if len(x_train) < min_samples_for_validation:
                    logger.warning(
                        f"Training data for '{feature}' has only {len(x_train)} samples. Disabling validation split."
                    )
                    effective_validation_split = 0.0

                modelobj = Model(
                    account_id=self.account,
                    tenant_id=self.tenant,
                    namespace=self.namespace,
                    deployment_name=self.deployment,
                    model_type=feature,
                )
                model = modelobj.get_model(x_train)
                model.fit(
                    x_train, y_train, epochs=epoch, batch_size=batch_size, validation_split=effective_validation_split
                )
                models[feature] = model
                modelobj.store_model(model=model)

            # Predict future values for each feature
            future_values = {}
            for feature in features:
                logger.info(f"Predicting future values for {feature}")
                last_sequence = sequences[feature][0][-1]  # Last sequence from the training data
                future_predictions = self.predict_future_values(models[feature], last_sequence, future_steps)
                future_values[feature] = scalars[feature].inverse_transform(future_predictions).flatten()

            # Release all trained models and TensorFlow session memory
            del models
            tf.keras.backend.clear_session()
            gc.collect()

            # Combine the historical data with the predicted future values to create a new dataset
            future_timestamps = np.array(
                [
                    ((pd.Timestamp(data["timestamp"].values[-1])) + timedelta(hours=i))
                    for i in range(len(future_values["rps"]))
                ]
            )
            future_data = pd.DataFrame(
                {
                    "timestamp": future_timestamps,
                    "rps": np.round(future_values["rps"]).astype(int),
                    "latency": np.round(future_values["latency"].astype(float), decimals=3),
                    "memory": np.round(np.array(future_values["memory"]).astype(float), decimals=2),
                    "cpu": np.round(np.array(future_values["cpu"]).astype(float), decimals=4),
                }
            )
            return future_data
        except Exception as e:
            msg = f"Failed to run model: exception: {e}"
            logger.exception(msg)
            tf.keras.backend.clear_session()
            gc.collect()
            raise ValueError(msg)

    def rule_based_model(self, data: pd.DataFrame) -> pd.DataFrame:
        k8s_res = K8sResource.get_from_db(id=UUID(self.resource_id))
        if [
            cont
            for cont in k8s_res.containers
            if cont.resources.limits.memory is None or cont.resources.limits.cpu is None
        ]:
            self.close_recommendation(resource_id=UUID(self.resource_id))
            raise CancelPrediction("Resource limits not set closing recommendation")
        max_mem = max(
            [cont.resources.limits.memory / (1024**2) for cont in k8s_res.containers if cont.resources.limits.memory]
        )
        max_cpu = max([cont.resources.limits.cpu for cont in k8s_res.containers if cont.resources.limits.cpu])
        if max_mem:
            data.loc[data["memory"] < (max_mem * 0.9), "replicas"] = 1
        if max_cpu:
            data.loc[data["cpu"] < max_cpu * 0.9, "replicas"] = 1
        return data

    def close_recommendation(self, resource_id: UUID):
        engine = DatabaseEngine.get_engine()
        query = text("""
            update
                recommendation
            set
                recommendation = jsonb_set(
                    recommendation,
                    '{recommendation,error}',
                    '"limits not set for resource"' :: jsonb
                )
            where
                resource_id = :resource_id
                and rule_name = 'replica_right_sizing'
            """)
        with engine.connect() as conn:
            conn.execute(query, {"resource_id": resource_id})
            conn.commit()
            conn.close()

    # Function to predict future values using the trained LSTM model
    def predict_future_values(self, model, last_sequence, steps):
        current_sequence = last_sequence[np.newaxis, :, :]
        future_predictions = []
        for _ in range(steps):
            next_prediction = model.predict(current_sequence)[0]
            future_predictions.append(next_prediction)
            current_sequence = np.roll(current_sequence, -1, axis=1)
            current_sequence[0, -1, 0] = next_prediction
        return np.array(future_predictions)

    def predict_replicas(
        self,
        data: pd.DataFrame,
        future_data: pd.DataFrame,
    ) -> pd.DataFrame:
        try:
            # Use historical data to predict replicas
            seq_length = get_prediction_initial_set(self.account)
            future_steps = get_prediction_step(self.account)
            epoch = int(ModelConfig.epoch)
            combined_data = pd.concat([data[["timestamp", "replicas"]], future_data.drop("timestamp", axis=1)], axis=1)
            # Replacing Nan to 0
            combined_data.fillna(0, inplace=True)
            # Scaling the combined data
            combined_data_values = combined_data.drop("timestamp", axis=1).values
            combined_scaler = MinMaxScaler()
            scaled_combined_data = combined_scaler.fit_transform(combined_data_values)

            # Create sequences for the replicas prediction
            x_combined, y_combined = self.create_sequences(scaled_combined_data, seq_length)

            # Split into train and validation sets for combined data
            train_size = int(len(x_combined) * 0.8)
            x_train_combined, x_val_combined = x_combined[:train_size], x_combined[train_size:]
            y_train_combined, y_val_combined = y_combined[:train_size], y_combined[train_size:]

            # Model for replicas
            # Build and train the multivariate LSTM model for replicas prediction
            model_obj = Model(
                account_id=self.account,
                tenant_id=self.tenant,
                namespace=self.namespace,
                deployment_name=self.deployment,
                model_type="replica",
            )
            model = model_obj.get_model(x_train_combined)
            batch_size = get_prediction_run_batch_size(self.account)

            history = model.fit(
                x_train_combined,
                y_train_combined[:, 0],
                epochs=epoch,
                batch_size=batch_size,
                validation_data=(x_val_combined, y_val_combined[:, 0]),
            )
            loss = np.max(history.history["val_loss"])
            if loss > 10:
                logger.error(f"Loss for the model {os.path.basename(model_obj.model_name)}: {loss}")
                raise ValueError(f"Loss for the model {os.path.basename(model_obj.model_name)}: {loss}")

            # Predict future replicas
            predicted_replicas = []
            current_sequence = x_combined[-1][np.newaxis, :, :]
            for _ in range(future_steps):
                next_prediction = model.predict(current_sequence)[0, 0]
                predicted_replicas.append(next_prediction)
                next_sequence = np.roll(current_sequence, -1, axis=1)
                next_sequence[0, -1, :-1] = current_sequence[0, -1, 1:]
                next_sequence[0, -1, -1] = next_prediction
                current_sequence = next_sequence

            # Inverse scaling the predictions
            predicted_replicas = combined_scaler.inverse_transform(
                np.concatenate(
                    (
                        np.array(predicted_replicas).reshape(-1, 1),
                        np.zeros((future_steps, scaled_combined_data.shape[1] - 1)),
                    ),
                    axis=1,
                )
            )[:, 0]
            # Creating a DataFrame for visualization
            logger.info("Saving the model")
            model_obj.store_model(model=model)
            logger.info("Deleting the model")
            del model
            tf.keras.backend.clear_session()
            gc.collect()
            timestamps_prediction = np.array(
                [((pd.Timestamp(data["timestamp"].values[-1])) + timedelta(hours=i)) for i in range(future_steps)]
            )
            df_forecast = pd.DataFrame(
                {"inference_time": timestamps_prediction, "replicas": np.ceil(predicted_replicas).astype(numpy.int64)}
            )
            df_forecast["tenant_id"] = self.tenant
            df_forecast["account_id"] = self.account
            df_forecast["namespace"] = self.namespace
            df_forecast["deployment"] = self.deployment

            df_forecast["cpu"] = future_data["cpu"]
            df_forecast["memory"] = future_data["memory"]
            df_forecast["latency"] = future_data["latency"]
            df_forecast["rps"] = future_data["rps"]

            df_forecast["resource_id"] = self.resource_id
            df_forecast["model"] = os.path.basename(model_obj.model_name)
            df_forecast["id"] = [str(uuid.uuid4()) for _ in range(len(df_forecast))]
            df_forecast = df_forecast[df_forecast.columns].fillna(0)
            # store_predictions(model_name, df_forecast)
            return df_forecast
        except Exception as e:
            msg = f"Failed to run model: exception: {e}"
            logger.error(msg)
            tf.keras.backend.clear_session()
            gc.collect()
            raise ValueError(msg)
